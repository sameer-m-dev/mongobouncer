package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/config"
)

func main() {
	// Define basic command-line flags
	var (
		configFile = flag.String("config", "", "Path to TOML configuration file")
		verbose    = flag.Bool("verbose", false, "Enable verbose (debug) logging")
		help       = flag.Bool("help", false, "Show help message")
	)
	flag.Parse()

	if *help {
		printUsage()
		return
	}

	// Load configuration
	c, err := config.LoadConfig(*configFile, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	run(c)
}

func printUsage() {
	fmt.Printf("Usage: %s [OPTIONS]\n", os.Args[0])
	fmt.Println()
	fmt.Println("OPTIONS:")
	fmt.Println("  --config string")
	fmt.Println("        Path to TOML configuration file")
	fmt.Println("  --verbose")
	fmt.Println("        Enable verbose (debug) logging")
	fmt.Println("  --help")
	fmt.Println("        Show this help message")
	fmt.Println()
	fmt.Println("CONFIGURATION:")
	fmt.Println("  All other configuration options should be specified in the TOML configuration file.")
	fmt.Println("  See examples/mongobouncer.example.toml for a complete configuration example.")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Printf("  %s --config /path/to/mongobouncer.toml\n", os.Args[0])
	fmt.Printf("  %s --config mongobouncer.toml -verbose\n", os.Args[0])
	fmt.Println()
}

func run(config *config.Config) {
	proxies, err := config.Proxies(config.Logger())
	log := config.Logger()

	if err != nil {
		log.Fatal("Startup error", zap.Error(err))
	}

	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
	}()

	for _, p := range proxies {
		p := p
		wg.Add(1)
		go func() {
			err := p.Run()
			if err != nil {
				log.Error("Error", zap.Error(err))
			}
			wg.Done()
		}()
	}

	shutdown := func() {
		for _, p := range proxies {
			p.Shutdown()
		}
	}
	kill := func() {
		for _, p := range proxies {
			p.Kill()
		}
	}
	shutdownOnSignal(log, shutdown, kill)

	log.Info("Running")
}

func shutdownOnSignal(log *zap.Logger, shutdownFunc func(), killFunc func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		shutdownAttempted := false
		for sig := range c {
			log.Info("Signal", zap.String("signal", sig.String()))

			if !shutdownAttempted {
				log.Info("Shutting down")
				go shutdownFunc()
				shutdownAttempted = true

				if sig == os.Interrupt {
					time.AfterFunc(1*time.Second, func() {
						fmt.Println("Ctrl-C again to kill incoming connections")
					})
				}
			} else if sig == os.Interrupt {
				log.Warn("Terminating")
				_ = log.Sync() // #nosec
				killFunc()
			}
		}
	}()
}
