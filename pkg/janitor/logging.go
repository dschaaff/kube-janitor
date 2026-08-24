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

// logLevel says what kind of thing a line reports. The levels are ordered from
// the one only a diagnosed run wants to the one no run can suppress, so a run
// keeps a single lowest level rather than one switch per level.
type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarning
	levelError
)

// String names the level the way Python's logging module does, because the log
// format is written in its placeholder syntax and %(levelname)s is expected to
// render the same words.
func (l logLevel) String() string {
	switch l {
	case levelDebug:
		return "DEBUG"
	case levelInfo:
		return "INFO"
	case levelWarning:
		return "WARNING"
	case levelError:
		return "ERROR"
	}
	return "UNKNOWN"
}

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

	// quoted says, per field, whether the format puts it between two double
	// quotes. Such a field is a string in whatever the format is building, so
	// its value is escaped rather than written through.
	quoted []bool
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
	f.markQuotedFields()
	return f, nil
}

// markQuotedFields works out which fields the format wrapped in quotes. It can
// only be decided once the whole format is compiled, because a field is quoted
// by the literal after it as much as by the literal before it.
func (f *logFormat) markQuotedFields() {
	f.quoted = make([]bool, len(f.fields))
	for i := range f.fields {
		f.quoted[i] = strings.HasSuffix(f.literals[i], `"`) &&
			strings.HasPrefix(f.literals[i+1], `"`)
	}
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

// logRecord is the one event a line reports, before the format has had its say.
type logRecord struct {
	level   logLevel
	message string
	now     time.Time
}

// value renders the one field of the record a placeholder names.
func (r logRecord) value(field logField) string {
	switch field {
	case fieldAsctime:
		return r.now.Format(asctimeLayout)
	case fieldCreated:
		return strconv.FormatFloat(float64(r.now.UnixNano())/1e9, 'f', 3, 64)
	case fieldLevelName:
		return r.level.String()
	case fieldMessage:
		return r.message
	case fieldName:
		return loggerName
	}
	return ""
}

// render builds one line from the compiled template.
func (f logFormat) render(level logLevel, message string, now time.Time) string {
	rec := logRecord{level: level, message: message, now: now}

	var line strings.Builder

	for i, field := range f.fields {
		line.WriteString(f.literals[i])

		if f.quoted[i] {
			writeQuotedValue(&line, rec.value(field))
		} else {
			line.WriteString(rec.value(field))
		}
	}

	line.WriteString(f.literals[len(f.literals)-1])
	return line.String()
}

// writeQuotedValue writes value as the body of a quoted string, escaping the
// characters that would otherwise end the string early or break the line in
// two. A format that wraps a field in quotes is building JSON or something
// shaped like it, and a message carrying a quote of its own — which one
// naming a namespace does — would leave it unparseable.
func writeQuotedValue(line *strings.Builder, value string) {
	for _, r := range value {
		switch {
		case r == '"':
			line.WriteString(`\"`)
		case r == '\\':
			line.WriteString(`\\`)
		case r == '\n':
			line.WriteString(`\n`)
		case r == '\r':
			line.WriteString(`\r`)
		case r == '\t':
			line.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(line, `\u%04x`, r)
		default:
			line.WriteRune(r)
		}
	}
}

// Logger writes one line per event, in the format the Configuration names.
//
// It also decides which lines are written at all, from the one lowest level the
// Configuration works out to. Nothing that emits a line checks whether the line
// is wanted.
type Logger struct {
	format logFormat
	min    logLevel

	// mu keeps one line from interleaving with another, which is the guarantee
	// the standard log package gave before. It only holds for lines written
	// through this Logger, so a process builds one and passes it around rather
	// than building a second onto the same writer.
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

	return &Logger{format: format, min: lowestLevel(cfg), out: out}
}

// lowestLevel works out the least a run reports. Asking for a diagnosis beats
// asking for quiet: a run given both is being diagnosed, and a diagnosis that
// left out the ordinary course of the run would be a poor one.
func lowestLevel(cfg *Config) logLevel {
	switch {
	case cfg.Debug:
		return levelDebug
	case cfg.Quiet:
		return levelWarning
	default:
		return levelInfo
	}
}

// Debugf writes a line a run only wants when it is being diagnosed.
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.write(levelDebug, format, args)
}

// Infof writes a line describing the ordinary course of a run.
func (l *Logger) Infof(format string, args ...interface{}) {
	l.write(levelInfo, format, args)
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
func (l *Logger) write(level logLevel, format string, args []interface{}) {
	if level < l.min {
		return
	}

	line := l.format.render(level, fmt.Sprintf(format, args...), time.Now())

	l.mu.Lock()
	defer l.mu.Unlock()

	// Nowhere left to report a writer that will not take a log line.
	_, _ = fmt.Fprintln(l.out, line)
}
