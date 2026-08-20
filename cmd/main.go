package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/edsrzf/mmap-go"
	"github.com/elfkuzco/zimfs/internal/fs"
	"github.com/jacobsa/fuse"
)

func main() {
	debugLog := log.New(os.Stdout, "DEBUG\t", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stderr, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// zimfs does not do its own access checking, so running as root would open
	// security holes. Refuse to mount as root.
	if (os.Getuid() == 0) || (os.Geteuid() == 0) {
		errorLog.Fatalln("running zimfs as root opens unacceptable security holes")
	}

	if len(os.Args) < 3 {
		errorLog.Fatalln("usage: zimfs <file.zim> <mountpoint>")
	}

	zimPath := os.Args[len(os.Args)-2]
	mountpoint := os.Args[len(os.Args)-1]

	f, err := os.Open(zimPath)
	if err != nil {
		errorLog.Fatalf("failed to open zim file at path %s: %v\n", zimPath, err)
	}
	defer f.Close()

	mapped, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		errorLog.Fatalf("failed to mmap zim file at path %s: %v\n", zimPath, err)
	}
	defer mapped.Unmap()

	server, err := fs.NewZimFS(mapped)
	if err != nil {
		errorLog.Fatalf("failed to create filesystem: %v\n", err)
	}

	mfs, err := fuse.Mount(mountpoint, server, &fuse.MountConfig{
		FSName:      "zimfs",
		ErrorLogger: errorLog,
		DebugLogger: debugLog,
	})
	if err != nil {
		errorLog.Fatalf("failed to mount at %s: %v\n", mountpoint, err)
	}

	if err := mfs.Join(context.Background()); err != nil {
		errorLog.Fatalf("mount ended with error: %v", err)
	}
}
