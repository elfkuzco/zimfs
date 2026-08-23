package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/edsrzf/mmap-go"
	"github.com/elfkuzco/zimfs/internal/fs"
	"github.com/jacobsa/fuse"
)

var (
	buildTime string // to hold the time the executable was built
	version   string // to hold the version number
)

type config struct {
	verbose bool
}

func printHelp() {
	fmt.Printf("zimfs - mount a ZIM file as a read-only filesystem\n\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  zimfs [options] <file.zim> <mountpoint>\n\n")
	fmt.Printf("Options:\n")
	flag.PrintDefaults()
}

func main() {

	var cfg config

	// set up the application logger
	var logLevel = new(slog.LevelVar)
	flag.BoolVar(&cfg.verbose, "verbose", false, "Enable verbose logging")

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
	// security holes. Refuse to mount as root.
	if (os.Getuid() == 0) || (os.Geteuid() == 0) {
		log.Fatal("running zimfs as root opens unacceptable security holes")
	}

	args := flag.Args()
	if len(args) < 2 {
		printHelp()
		os.Exit(1)
	}

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

	if err := mfs.Join(context.Background()); err != nil {
		log.Fatalf("mount ended with error: %v", err)
	}
}
