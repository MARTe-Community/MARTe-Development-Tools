package logger

import (
	"io"
	"log"
	"os"
)

// ANSI escape codes for coloring terminal output.
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorGreen  = "\033[32m"
)

var (
	// Default logger writes to stderr
	std = log.New(os.Stderr, "[mdt] ", log.LstdFlags)
	// EnableColors controls whether to use ANSI escape codes.
	EnableColors = checkTerminal(os.Stderr)
)

func checkTerminal(w io.Writer) bool {
	// Respect NO_COLOR standard
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	// Check if the writer is a terminal
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err == nil {
			return (stat.Mode() & os.ModeCharDevice) != 0
		}
	}
	return false
}

// Colorize wraps the string with the specified ANSI color code if colors are enabled.
func Colorize(str, color string) string {
	if !EnableColors {
		return str
	}
	return color + str + ColorReset
}

func SetOutput(output io.Writer) {
	std.SetOutput(output)
	// Re-evaluate if colors should be enabled for the new output
	EnableColors = checkTerminal(output)
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

// Info logs a message with a green info prefix.
func Info(v ...interface{}) {
	std.Println(append([]interface{}{Colorize("[INFO]", ColorGreen)}, v...)...)
}

// Infof logs a formatted message with a green info prefix.
func Infof(format string, v ...interface{}) {
	std.Printf(Colorize("[INFO]", ColorGreen)+" "+format, v...)
}

// Warning logs a message with a yellow warning prefix.
func Warning(v ...interface{}) {
	std.Println(append([]interface{}{Colorize("[WARN]", ColorYellow)}, v...)...)
}

// Warningf logs a formatted message with a yellow warning prefix.
func Warningf(format string, v ...interface{}) {
	std.Printf(Colorize("[WARN]", ColorYellow)+" "+format, v...)
}

// Error logs a message with a red error prefix.
func Error(v ...interface{}) {
	std.Println(append([]interface{}{Colorize("[ERROR]", ColorRed)}, v...)...)
}

// Errorf logs a formatted message with a red error prefix.
func Errorf(format string, v ...interface{}) {
	std.Printf(Colorize("[ERROR]", ColorRed)+" "+format, v...)
}
