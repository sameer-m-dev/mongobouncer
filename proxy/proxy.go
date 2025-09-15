package proxy

import (
	"fmt"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/util"
	"go.uber.org/zap"
)

const restartSleep = 1 * time.Second

type Proxy struct {
	log     *zap.Logger
	metrics util.MetricsInterface

	network string
	address string
	unlink  bool

	poolManager                *pool.Manager
	databaseRouter             *DatabaseRouter
	authEnabled                bool
	regexCredentialPassthrough bool

	// MongoDB client settings for dynamic client creation
	mongodbDefaults util.MongoDBClientConfig // Default MongoDB client settings

	quit chan interface{}
	kill chan interface{}
}

func NewProxy(log *zap.Logger, metrics util.MetricsInterface, label, network, address string, unlink bool, poolManager *pool.Manager, databaseRouter *DatabaseRouter, authEnabled bool, regexCredentialPassthrough bool, mongodbDefaults util.MongoDBClientConfig) (*Proxy, error) {
	if label != "" {
		log = log.With(zap.String("cluster", label))
	}
	return &Proxy{
		log:     log,
		metrics: metrics,

		network: network,
		address: address,
		unlink:  unlink,

		poolManager:                poolManager,
		databaseRouter:             databaseRouter,
		authEnabled:                authEnabled,
		regexCredentialPassthrough: regexCredentialPassthrough,
		mongodbDefaults:            mongodbDefaults,

		quit: make(chan interface{}),
		kill: make(chan interface{}),
	}, nil
}

func (p *Proxy) Run() error {
	return p.run()
}

func (p *Proxy) Shutdown() {
	defer func() {
		_ = recover() // "close of closed channel" panic if Shutdown() was already called
	}()
	close(p.quit)
}

func (p *Proxy) Kill() {
	p.Shutdown()

	defer func() {
		_ = recover() // "close of closed channel" panic if Kill() was already called
	}()
	close(p.kill)
}

func (p *Proxy) run() error {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("Crashed", zap.String("panic", fmt.Sprintf("%v", r)), zap.String("stack", string(debug.Stack())))

			time.Sleep(restartSleep)

			p.log.Info("Restarting", zap.Duration("sleep", restartSleep))
			go func() {
				err := p.run()
				if err != nil {
					p.log.Error("Error restarting", zap.Error(err))
				}
			}()
		}
	}()

	return p.listen()
}

func (p *Proxy) listen() error {
	if strings.Contains(p.network, "unix") {
		oldUmask := syscall.Umask(0)
		defer syscall.Umask(oldUmask)
		if p.unlink {
			_ = syscall.Unlink(p.address)
		}
	}

	l, err := net.Listen(p.network, p.address)
	if err != nil {
		return err
	}
	defer func() {
		_ = l.Close()
	}()
	go func() {
		<-p.quit
		err := l.Close()
		if err != nil {
			p.log.Info("Error closing listener", zap.Error(err))
		}
	}()

	p.accept(l)
	return nil
}

func (p *Proxy) accept(l net.Listener) {
	var wg sync.WaitGroup
	defer func() {
		p.log.Info("Waiting for open connections")
		wg.Wait()
	}()

	var opened, closed util.BackgroundGaugeCallback
	if p.metrics != nil {
		opened, closed = p.metrics.BackgroundGauge("open_connections", []string{})
	} else {
		// No-op callbacks if metrics are disabled
		opened = func(string, []string) {}
		closed = func(string, []string) {}
	}

	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-p.quit:
				return
			default:
				p.log.Error("Failed to accept incoming connection", zap.Error(err))
				continue
			}
		}

		log := p.log
		remoteAddr := c.RemoteAddr().String()
		if remoteAddr != "" {
			log = p.log.With(zap.String("remote_address", remoteAddr))
		}

		done := make(chan interface{})

		wg.Add(1)
		opened("connection_opened", []string{})
		go func() {
			log.Debug("Accepting new client connection",
				zap.String("local_address", p.address),
				zap.String("network", p.network))

			// Call handleConnection which now returns the database used
			databaseUsed := handleConnection(log, p.metrics, p.address, c, p.poolManager, p.kill, p.databaseRouter, p.authEnabled, p.regexCredentialPassthrough, p.mongodbDefaults)

			_ = c.Close()
			log.Debug("Client connection closed",
				zap.String("remote_address", remoteAddr),
				zap.String("local_address", p.address),
				zap.String("database_used", databaseUsed))

			close(done)
			wg.Done()
			closed("connection_closed", []string{})
		}()

		go func() {
			select {
			case <-done:
				// closed
			case <-p.kill:
				err := c.Close()
				if err == nil {
					log.Warn("Force closed connection")
				}
			}
		}()
	}
}
