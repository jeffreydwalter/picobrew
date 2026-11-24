package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/server"
)

func main() {
	if len(os.Args) > 1 {
		var command func([]string) error
		switch os.Args[1] {
		case "usb-backup":
			command = runUSBBackupCommand
		case "usb-install":
			command = runUSBInstallCommand
		case "usb-restore":
			command = runUSBRestoreCommand
		}
		if command != nil {
			if err := command(os.Args[2:]); err != nil && !errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
	listen := flag.String("listen", "0.0.0.0:8080", "HTTP listen address (demo defaults to 127.0.0.1:8080)")
	deviceURL := flag.String("device-url", os.Getenv("HUB_DEVICE_URL"), "Optional HTTP URL by which machines reach this Hub (normally detected automatically)")
	data := flag.String("data", "data", "Persistent data directory")
	web := flag.String("web", "", "Development override for web assets (default: website bundled in the binary)")
	version := flag.Bool("version", false, "Print the Hub version and exit")
	demo := flag.Bool("demo", false, "Use local simulation without hardware access")
	secure := flag.Bool("secure-cookies", false, "Require HTTPS for owner cookies (enable behind TLS proxy)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: picobrew-hub [server options]\n       picobrew-hub usb-install --help\n       picobrew-hub usb-backup --help\n       picobrew-hub usb-restore --help")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *version {
		fmt.Println(server.Version)
		return
	}
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	explicitListen := false
	flag.Visit(func(f *flag.Flag) { explicitListen = explicitListen || f.Name == "listen" })
	address, err := selectListenAddress(*listen, *demo, explicitListen)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	*listen = address
	listener, e := net.Listen("tcp", *listen)
	if e != nil {
		slog.Error("Could not listen", "error", e)
		os.Exit(1)
	}
	app, e := server.New(server.Config{DataDir: *data, WebDir: *web, Password: os.Getenv("HUB_PASSWORD"), Demo: *demo, SecureCookies: *secure, ListenAddress: listener.Addr().String(), DeviceURL: *deviceURL})
	if e != nil {
		_ = listener.Close()
		slog.Error("Startup failed", "error", e)
		os.Exit(1)
	}
	httpServer := &http.Server{Addr: *listen, Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10}
	httpServer.RegisterOnShutdown(app.BeginShutdown)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	exitCode := 0
	go func() {
		slog.Info("PicoBrew Hub ready", "address", listener.Addr().String(), "version", server.Version, "simulation", *demo)
		done <- httpServer.Serve(listener)
	}()
	select {
	case e = <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			slog.Error("Server failed", "error", e)
			exitCode = 1
		}
	case <-ctx.Done():
		// Restore the default signal behavior so another Ctrl-C can interrupt
		// a request that is taking too long to finish its graceful shutdown.
		stop()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		e = httpServer.Shutdown(shutdown)
		cancel()
		if e != nil {
			_ = httpServer.Close()
		}
	}
	if e = app.Close(); e != nil {
		slog.Error("Close failed", "error", e)
		exitCode = 1
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func selectListenAddress(address string, demo, explicit bool) (string, error) {
	if !demo {
		return address, nil
	}
	if !explicit {
		return "127.0.0.1:8080", nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return "", errors.New("demo must bind to loopback; run authenticated mode for network deployment")
	}
	return address, nil
}
