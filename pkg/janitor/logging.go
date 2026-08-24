package janitor

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// loggerName is what %(name)s renders: the program a log line came from.
const loggerName = "kube-janitor"

// asctimeLayout is what %(asctime)s renders. It is the layout the janitor has
// always printed, so a default format produces the line it always did.
const asctimeLayout = "2006-01-02 15:04:05"

// The levels a line can carry. They are the names Python's logging module uses,
// because the log format is written in its placeholder syntax and %(levelname)s
// is expected to render the same words.
const (
	levelDebug   = "DEBUG"
	levelInfo    = "INFO"
	levelWarning = "WARNING"
	levelError   = "ERROR"
)

// logField is one value a log format interpolates.
type logField int

const (
	fieldAsctime logField = iota
	fieldCreated
	fieldLevelName
	fieldMessage
	fieldName
)

// logFields names every placeholder a format may use. A name that is not here
// is refused when the configuration loads rather than rendered as empty, so a
// format that would silently drop a field never reaches a run.
var logFields = map[string]logField{
	"asctime":   fieldAsctime,
	"created":   fieldCreated,
	"levelname": fieldLevelName,
	"message":   fieldMessage,
	"name":      fieldName,
}

// logFormat is a compiled log line template: the literal text around the
// placeholders, and the field that fills each gap. Compiling once means a run
// does no parsing per line.
type logFormat struct {
	// literals holds one more entry than fields: the text before each field,
	// then the text after the last one.
	literals []string
	fields   []logField
}

// parseLogFormat compiles a log format written in Python's logging syntax,
// where %(name)s names a field. It reports what is wrong with a format it
// cannot compile, and which fields it would have accepted.
func parseLogFormat(format string) (logFormat, error) {
	var f logFormat
	var literal strings.Builder

	for i := 0; i < len(format); {
		// %% is a literal percent, as it is in the Python format this syntax
		// comes from, so a format ported from there reads the same.
		if strings.HasPrefix(format[i:], "%%") {
			literal.WriteByte('%')
			i += 2
			continue
		}

		if !strings.HasPrefix(format[i:], "%(") {
			literal.WriteByte(format[i])
			i++
			continue
		}

		end := strings.Index(format[i:], ")")
		if end < 0 {
			return logFormat{}, fmt.Errorf("log format has an unclosed placeholder: %q", format[i:])
		}

		name := format[i+2 : i+end]
		if !strings.HasPrefix(format[i+end+1:], "s") {
			return logFormat{}, fmt.Errorf("log format placeholder %%(%s) must be written %%(%s)s", name, name)
		}

		field, known := logFields[name]
		if !known {
			return logFormat{}, fmt.Errorf("log format has unknown placeholder %%(%s)s, want one of %s",
				name, strings.Join(knownLogFields(), ", "))
		}

		f.literals = append(f.literals, literal.String())
		literal.Reset()
		f.fields = append(f.fields, field)
		i += end + 2
	}

	f.literals = append(f.literals, literal.String())
	return f, nil
}

// knownLogFields names the placeholders a format may use, in a stable order so
// that an error message reads the same every time.
func knownLogFields() []string {
	names := make([]string, 0, len(logFields))
	for name := range logFields {
		names = append(names, "%("+name+")s")
	}
	slices.Sort(names)
	return names
}

// render builds one line from the compiled template.
func (f logFormat) render(level, message string, now time.Time) string {
	var line strings.Builder

	for i, field := range f.fields {
		line.WriteString(f.literals[i])

		switch field {
		case fieldAsctime:
			line.WriteString(now.Format(asctimeLayout))
		case fieldCreated:
			line.WriteString(strconv.FormatFloat(float64(now.UnixNano())/1e9, 'f', 3, 64))
		case fieldLevelName:
			line.WriteString(level)
		case fieldMessage:
			line.WriteString(message)
		case fieldName:
			line.WriteString(loggerName)
		}
	}

	line.WriteString(f.literals[len(f.literals)-1])
	return line.String()
}

// Logger writes one line per event, in the format the Configuration names.
//
// It also decides which lines are written at all: debug lines only when debug
// is on, and info lines unless the run is quiet. Warnings and errors are always
// written, so a quiet run still reports what went wrong.
type Logger struct {
	format logFormat
	debug  bool
	quiet  bool

	// mu keeps one line from interleaving with another, which is the guarantee
	// the standard log package gave before.
	mu  sync.Mutex
	out io.Writer
}

// NewLogger builds the Logger a Configuration asks for, writing to out.
//
// A Config that came from LoadConfig carries a format that is already known to
// compile. A Config built by hand may carry none, or one that does not compile,
// and gets the default format rather than an unusable Logger.
func NewLogger(cfg *Config, out io.Writer) *Logger {
	format, err := parseLogFormat(cfg.LogFormat)
	if cfg.LogFormat == "" || err != nil {
		format, _ = parseLogFormat(defaultLogFormat)
	}

	return &Logger{format: format, debug: cfg.Debug, quiet: cfg.Quiet, out: out}
}

// Debugf writes a line a run only wants when it is being diagnosed.
func (l *Logger) Debugf(format string, args ...interface{}) {
	if l.debug {
		l.write(levelDebug, format, args)
	}
}

// Infof writes a line describing the ordinary course of a run.
func (l *Logger) Infof(format string, args ...interface{}) {
	if !l.quiet {
		l.write(levelInfo, format, args)
	}
}

// Warnf writes a line about something a run worked around.
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.write(levelWarning, format, args)
}

// Errorf writes a line about something a run could not do.
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.write(levelError, format, args)
}

// write renders one line and puts it out whole. The message is formatted only
// once the level has been admitted, so a suppressed line costs nothing beyond
// the call.
func (l *Logger) write(level, format string, args []interface{}) {
	line := l.format.render(level, fmt.Sprintf(format, args...), time.Now())

	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Fprintln(l.out, line)
}
