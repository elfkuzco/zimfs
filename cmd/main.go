package main

import (
	"log"
	"os"
)

func main() {
	// logger for writing INFO level messages
	infoLog := log.New(os.Stdout, "INFO\t", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stderr, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile)
	// zimfs doesn't do any access checking on its own (the comment
	// blocks in fuse.h mention some of the functions that need
	// accesses checked -- but note there are other functions, like
	// chown(), that also need checking!).  Since running zimfs as
	// root will therefore open Metrodome-sized holes in the system
	// security, we'll check if root is trying to mount the
	// filesystem and refuse if it is.  The somewhat smaller hole of
	// an ordinary user doing it with the allow_other flag is still
	// there because I don't want to parse the options string.
	if (os.Getuid() == 0) || (os.Geteuid() == 0) {
		errorLog.Fatalln(
			"Running zimfs as root opens unnacceptable security holes",
		)
	}

	// Perform some sanity checking on the command line:  make sure
	// there are enough arguments, and that neither of the last two
	// start with a hyphen (this will break if you actually have a
	// rootpoint or mountpoint whose name starts with a hyphen, but
	// so will a zillion other programs)
	argc := len(os.Args)
	if (argc < 3) || (os.Args[argc-2][0] == '-') ||
		(os.Args[argc-1][0] == '-') {
		infoLog.Printf("zimfile error")
		// zimfsUsage();
		os.Exit(1)
	}

}
