package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edsrzf/mmap-go"
	"github.com/elfkuzco/zimfs/internal/fs"
	"github.com/jacobsa/fuse"
	daemon "github.com/sevlyar/go-daemon"
)

var (
	buildTime string // to hold the time the executable was built
	version   string // to hold the version number
)

type config struct {
	verbose    bool
	foreground bool
	allowRoot  bool
}

func printHelp() {
	fmt.Printf("zimfs - mount a ZIM file as a read-only filesystem\n\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  zimfs [options] <file.zim> <mountpoint>\n\n")
	fmt.Printf("Options:\n")
	flag.PrintDefaults()
}

func unmountWithRetry(mountPoint string) error {
	const (
		maxAttempts = 5
		backoff     = 200 * time.Millisecond
	)

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err = fuse.Unmount(mountPoint); err == nil {
			log.Printf("successfully unmounted %v", mountPoint)
			return nil
		}
		log.Printf("unmount attempt %d/%d failed: %v", attempt, maxAttempts, err)
		time.Sleep(backoff * time.Duration(attempt))
	}

	return fmt.Errorf("failed to unmount %s after %d attempts: %w", mountPoint, maxAttempts, err)
}

func main() {

	var cfg config

	// set up the application logger
	var logLevel = new(slog.LevelVar)
	flag.BoolVar(&cfg.verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&cfg.foreground, "foreground", false, "Run in foreground")
	flag.BoolVar(&cfg.allowRoot, "allow-root", false, "Allow running as root (use with caution)")

	// boolean to display version
	displayVersion := flag.Bool("version", false, "Display version and exit")
	flag.Usage = printHelp

	flag.Parse()

	// If the version flag is true, print the version number and exit
	if *displayVersion {
		fmt.Printf("Version:\t%s\n", version)
		fmt.Printf("Build time:\t%s\n", buildTime)
		os.Exit(0)
	}

	if cfg.verbose {
		logLevel.Set(slog.LevelDebug)
	} else {
		logLevel.Set(slog.LevelInfo)
	}

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// zimfs does not do its own access checking, so running as root would open
	// security holes. Refuse to mount as root unless explicitly opted in.
	if (os.Getuid() == 0 || os.Geteuid() == 0) && !cfg.allowRoot {
		log.Fatal("running zimfs as root opens unacceptable security holes (use --allow-root to override)")
	}

	args := flag.Args()
	if len(args) < 2 {
		printHelp()
		os.Exit(1)
	}

	if !cfg.foreground {
		ctx := new(daemon.Context)
		child, err := ctx.Reborn()

		if err != nil {
			log.Fatalf("unable to daemonize: %v", err)
		}

		if child != nil {
			return
		}
	}

	// Set up a context that is cancelled on SIGINT/SIGTERM so the mount can
	// be torn down cleanly.
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	zimPath := args[0]
	mountpoint := args[1]

	f, err := os.Open(zimPath)
	if err != nil {
		log.Fatalf("failed to open zim file at path %s: %v\n", zimPath, err)
	}
	defer f.Close()

	mapped, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		log.Fatalf("failed to mmap zim file at path %s: %v\n", zimPath, err)
	}
	defer mapped.Unmap()

	server, err := fs.NewZimFS(mapped)
	if err != nil {
		log.Fatalf("failed to create filesystem: %v\n", err)
	}

	fuseLogger := slog.NewLogLogger(handler, logLevel.Level())

	mfs, err := fuse.Mount(mountpoint, server, &fuse.MountConfig{
		FSName:      "zimfs",
		ErrorLogger: fuseLogger,
		WireLogger:  fuseLogger.Writer(),
		ReadOnly:    true,
	})
	if err != nil {
		log.Fatalf("failed to mount at %s: %v\n", mountpoint, err)
	}

	log.Printf("filesystem successfully mounted at %v\n", mountpoint)

	err = mfs.Join(signalCtx)
	switch {
	case errors.Is(err, context.Canceled):
		if uerr := unmountWithRetry(mountpoint); uerr != nil {
			log.Fatalf("failed to unmount %s: %v", mountpoint, uerr)
		}
	case err != nil:
		log.Fatalf("mount ended with error: %v", err)
	}
}
