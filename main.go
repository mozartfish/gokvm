package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/bobuhiro11/gokvm/flag"
	"github.com/bobuhiro11/gokvm/kvm"
	"github.com/bobuhiro11/gokvm/machine"
	"github.com/bobuhiro11/gokvm/probe"
	"github.com/bobuhiro11/gokvm/term"
)

func main() {
	// This line break is required by golangci-lint but
	// such breaks are considered an anti-pattern
	// at Google.
	c, err := flag.ParseArgs(os.Args)
	if err != nil {
		log.Fatalf("ParseArgs: %v", err)
	}

	if c.Debug {
		if err := probe.KVMCapabilities(); err != nil {
			log.Fatalf("kvm capabilities: %v", err)
		}
	}

	m, err := machine.New(c.Dev, c.NCPUs, c.MemSize)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if len(c.TapIfName) > 0 {
		if err := m.AddTapIf(c.TapIfName); err != nil {
			log.Fatalf("%v", err)
		}
	}

	if len(c.Disk) > 0 {
		if err := m.AddDisk(c.Disk); err != nil {
			log.Fatalf("%v", err)
		}
	}

	kern, err := os.Open(c.Kernel)
	if err != nil {
		log.Fatal(err)
	}

	initrd, err := os.Open(c.Initrd)
	if err != nil {
		log.Fatal(err)
	}

	if err := m.LoadLinux(kern, initrd, c.Params); err != nil {
		log.Fatalf("%v", err)
	}

	var wg sync.WaitGroup

	trace := c.TraceCount > 0
	if err := m.SingleStep(trace); err != nil {
		log.Fatalf("Setting trace to %v:%v", trace, err)
	}

	for cpu := 0; cpu < c.NCPUs; cpu++ {
		fmt.Printf("Start CPU %d of %d\r\n", cpu, c.NCPUs)
		wg.Add(1)

		go func(cpu int) {
			// Consider ANOTHER option, maxInsCount, which would
			// exit this loop after a certain number of instructions
			// were run.
			for tc := 0; ; tc++ {
				err = m.RunInfiniteLoop(cpu)
				if err == nil {
					continue
				}

				if !errors.Is(err, kvm.ErrDebug) {
					break
				}

				if err := m.SingleStep(trace); err != nil {
					log.Fatalf("Setting trace to %v:%v", trace, err)
				}

				if tc%c.TraceCount != 0 {
					continue
				}

				_, r, s, err := m.Inst(cpu)
				if err != nil {
					fmt.Printf("disassembling after debug exit:%v", err)
				} else {
					fmt.Printf("%#x:%s\r\n", r.RIP, s)
				}
			}

			wg.Done()
			fmt.Printf("CPU %d exits\n\r", cpu)
		}(cpu)
	}

	if !term.IsTerminal() {
		fmt.Fprintln(os.Stderr, "this is not terminal and does not accept input")
		select {}
	}

	restoreMode, err := term.SetRawMode()
	if err != nil {
		log.Fatalf("%v", err)
	}

	defer restoreMode()

	var before byte = 0

	in := bufio.NewReader(os.Stdin)

	if err := m.SingleStep(trace); err != nil {
		log.Printf("SingleStep(%v): %v", trace, err)

		return
	}

	go func() {
		for {
			b, err := in.ReadByte()
			if err != nil {
				log.Printf("%v", err)

				break
			}
			m.GetInputChan() <- b

			if len(m.GetInputChan()) > 0 {
				if err := m.InjectSerialIRQ(); err != nil {
					log.Printf("InjectSerialIRQ: %v", err)
				}
			}

			if before == 0x1 && b == 'x' {
				restoreMode()
				os.Exit(0)
			}

			before = b
		}
	}()

	fmt.Printf("Waiting for CPUs to exit\r\n")
	wg.Wait()
	fmt.Printf("All cpus done\n\r")
}
