package logger

import (
	"log"
	"os"
)

var (
	// Default logger writes to stderr
	std = log.New(os.Stderr, "[mdt] ", log.LstdFlags)
)

func SetOutput(output *os.File) {
	std.SetOutput(output)
}

func Printf(format string, v ...interface{}) {
	std.Printf(format, v...)
}

func Println(v ...interface{}) {
	std.Println(v...)
}

func Fatal(v ...interface{}) {
	std.Fatal(v...)
}

func Fatalf(format string, v ...interface{}) {
	std.Fatalf(format, v...)
}
