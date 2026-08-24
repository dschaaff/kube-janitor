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
	// literals holds one more entry than placeholders: the text before each
	// one, then the text after the last one.
	literals     []string
	placeholders []placeholder
}

// placeholder is one gap a format leaves: the field that fills it, and whether
// the format put that field between two double quotes. A quoted field is a
// string in whatever the format is building, so its value is escaped rather
// than written through.
type placeholder struct {
	field  logField
	quoted bool
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
		f.placeholders = append(f.placeholders, placeholder{field: field})
		i += end + 2
	}

	f.literals = append(f.literals, literal.String())
	f.markQuotedPlaceholders()
	return f, nil
}

// markQuotedPlaceholders works out which fields the format wrapped in quotes.
// It can only be decided once the whole format is compiled, because a field is
// quoted by the literal after it as much as by the literal before it.
//
// Keying on the quotes rather than on the shape as a whole is what lets one
// rule serve JSON and the logfmt-style formats alike. It is deliberately the
// only structure this reads out of a format: a second such inference would
// mean the format should name its shape instead of implying it.
func (f *logFormat) markQuotedPlaceholders() {
	for i := range f.placeholders {
		f.placeholders[i].quoted = strings.HasSuffix(f.literals[i], `"`) &&
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
		return programName
	}
	return ""
}

// render builds one line from the compiled template.
func (f logFormat) render(level logLevel, message string, now time.Time) string {
	rec := logRecord{level: level, message: message, now: now}

	var line strings.Builder

	for i, p := range f.placeholders {
		line.WriteString(f.literals[i])

		value := rec.value(p.field)
		if p.quoted {
			writeQuotedValue(&line, value)
		} else {
			line.WriteString(value)
		}
	}

	line.WriteString(f.literals[len(f.literals)-1])
	return line.String()
}

// hexDigits spells the \u escape writeQuotedValue emits for a control
// character.
const hexDigits = "0123456789abcdef"

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
			// Written out rather than handed to fmt: taking an io.Writer here
			// would move the whole line's Builder to the heap, on every line.
			// r < 0x20 means the top two nibbles are zero.
			line.WriteString(`\u00`)
			line.WriteByte(hexDigits[r>>4])
			line.WriteByte(hexDigits[r&0xf])
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

	return &Logger{format: format, min: cfg.lowestLogLevel(), out: out}
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
