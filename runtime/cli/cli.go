// Package cli is the runtime of command-line clients goapi generates: a
// command per operation, whose flags it reads into the operation's
// request, which it sends with the generated client, printing the
// response. Generated code describes the commands; see App.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dheins/go-openapi/runtime/client"
)

// Exit codes: a declared success response, another declared response,
// and anything else (usage, invalid requests, transport errors and
// responses the document does not declare).
const (
	ExitOK       = 0
	ExitResponse = 1
	ExitFailure  = 2
)

// Kind says how a parameter's flag is read.
type Kind int

const (
	// Text is one value, as its text: a string, a number, a boolean, or
	// a type that implements encoding.TextUnmarshaler.
	Text Kind = iota
	// List is an array: a repeated flag, or one of comma-separated items.
	List
	// JSON is an object, or a value with JSON content, as JSON.
	JSON
)

// Param is a parameter of a command's operation.
type Param struct {
	// Flag is the flag's name, Field the request's field, In and Name
	// where the parameter is sent, and Help what it is.
	Flag, Field, In, Name, Help string
	Kind                        Kind
	Required                    bool
}

// Media is a media type a command's body may be sent as.
type Media struct {
	MediaType string
	// Kind is json, xml, form, codec or multipart (the body is its JSON),
	// text, binary, parts or partstream (positional parts, given with
	// -part or as a multipart document), jsonl or sse.
	Kind string
	// Field is the request's field holding the body.
	Field string
	// Files are the file properties of a multipart body.
	Files []File
	// Stream, for a JSON Lines or event stream body, returns the stream
	// of the field's type reading the body from r as it is sent: one of
	// JSONLines, Events or TypedEvents.
	Stream func(r io.Reader) any
}

// File is a file property of a multipart body: its flag, and the JSON
// name and Go field of the property.
type File struct {
	Flag, Property, Field string
}

// Command is the command of one operation, sent with a client of type C.
type Command[C any] struct {
	Name, Summary, Tag string
	// Request returns a new request of the operation.
	Request func() any
	Params  []Param
	// Media are the media types of the body, the first the default; the
	// operation has none without a body. BodyRequired says one must be
	// given.
	Media        []Media
	BodyRequired bool
	// ContentType, if set, is the request's field naming the media type
	// the body is sent as, for operations with several.
	ContentType string
	// Call sends the request with c.
	Call func(ctx context.Context, c C, req any) (any, error)
}

// Scheme is a security scheme whose credentials are given with a flag
// and an environment variable: a value (an API key or a token), or with
// Basic a user name and password (Flag-user, Flag-password).
type Scheme struct {
	Flag, Env, Help string
	Basic           bool
}

// Credential is what was given for a scheme.
type Credential struct {
	Value, User, Password string
}

// App is a command-line client.
type App[C any] struct {
	// Name is the program's name, in usage and as the prefix of its
	// environment variables, and Summary what it is a client of.
	Name, Summary string
	// Server is the default server URL, or "".
	Server   string
	Schemes  []Scheme
	Commands []Command[C]
	// New returns a client sending the given credentials, by scheme flag.
	New func(creds map[string]Credential, opts ...client.Option) (C, error)
}

// Run runs the command of args, writing the response to stdout and
// problems to stderr, and returns the exit code.
func Run[C any](app App[C], args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 {
			if cmd := app.find(args[1]); cmd != nil {
				fs, _ := app.flags(cmd, stderr)
				fs.Usage()
				return ExitOK
			}
		}
		app.usage(stdout)
		if len(args) == 0 {
			return ExitFailure
		}
		return ExitOK
	}
	cmd := app.find(args[0])
	if cmd == nil {
		fmt.Fprintf(stderr, "%s: unknown command %q; see %s help\n", app.Name, args[0], app.Name)
		return ExitFailure
	}
	fs, g := app.flags(cmd, stderr)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitFailure
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s %s: unexpected arguments %q\n", app.Name, cmd.Name, fs.Args())
		return ExitFailure
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "%s %s: %v\n", app.Name, cmd.Name, err)
		return ExitFailure
	}
	req, err := request(g, cmd)
	defer g.close(stderr)
	if err != nil {
		return fail(err)
	}
	server := g.server
	if server == "" {
		server = os.Getenv(app.env("SERVER"))
	}
	base := client.WithBaseURL(server)
	if server == "" {
		// The document's server, whose operations' own servers apply.
		base = client.WithDefaultBaseURL(app.Server)
	}
	capture := &captured{}
	opts := []client.Option{base, client.WithMiddleware(capture.middleware)}
	for _, h := range g.headers {
		name, value, ok := strings.Cut(h, ":")
		if !ok {
			return fail(fmt.Errorf("-header %q is not name:value", h))
		}
		opts = append(opts, client.WithHeader(strings.TrimSpace(name), strings.TrimSpace(value)))
	}
	c, err := app.New(app.credentials(g), opts...)
	if err != nil {
		return fail(err)
	}
	ctx := context.Background()
	if g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.timeout)
		defer cancel()
	}
	resp, err := cmd.Call(ctx, c, req)
	if g.verbose && capture.resp != nil {
		capture.describe(stderr)
	}
	if err != nil {
		return failure(app.Name+" "+cmd.Name, err, stdout, stderr)
	}
	if err := print(resp, capture, stdout); err != nil {
		return fail(err)
	}
	if capture.resp == nil || capture.resp.StatusCode < 200 || capture.resp.StatusCode > 299 {
		return ExitResponse
	}
	return ExitOK
}

func (app App[C]) find(name string) *Command[C] {
	for i := range app.Commands {
		if app.Commands[i].Name == name {
			return &app.Commands[i]
		}
	}
	return nil
}

// env returns the environment variable of name, prefixed with the app's.
func (app App[C]) env(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(app.Name + "_" + name))
}

// usage lists the commands by tag.
func (app App[C]) usage(w io.Writer) {
	fmt.Fprintf(w, "%s is a command-line client of %s.\n\nUsage: %s <command> [flags]\n", app.Name, app.Summary, app.Name)
	var tags []string
	byTag := map[string][]*Command[C]{}
	for i := range app.Commands {
		cmd := &app.Commands[i]
		if _, ok := byTag[cmd.Tag]; !ok {
			tags = append(tags, cmd.Tag)
		}
		byTag[cmd.Tag] = append(byTag[cmd.Tag], cmd)
	}
	for _, tag := range tags {
		title := tag
		if title == "" {
			title = "commands"
		}
		fmt.Fprintf(w, "\n%s:\n", title)
		for _, cmd := range byTag[tag] {
			fmt.Fprintf(w, "  %-28s %s\n", cmd.Name, cmd.Summary)
		}
	}
	fmt.Fprintf(w, "\nRun %s help <command> for its flags. The server is -server, %s, or %s by default.\n", app.Name, app.env("SERVER"), app.Server)
}

// given are the flags of a command, as parsed.
type given struct {
	fs          *flag.FlagSet
	params      map[string]*multi
	body        string
	contentType string
	files       map[string]string
	parts       []partArg
	// closers are the sources of streamed bodies, closed when the
	// command is done.
	closers []io.Closer
	server  string
	headers multi
	timeout time.Duration
	verbose bool
	creds   map[string]*Credential
}

// close closes the sources of streamed bodies.
func (g *given) close(stderr io.Writer) {
	for _, c := range g.closers {
		if err := c.Close(); err != nil {
			fmt.Fprintf(stderr, "closing the body: %v\n", err)
		}
	}
}

// flags returns the flag set of cmd: its parameters and body, then the
// flags every command has.
func (app App[C]) flags(cmd *Command[C], stderr io.Writer) (*flag.FlagSet, *given) {
	fs := flag.NewFlagSet(app.Name+" "+cmd.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	g := &given{fs: fs, params: map[string]*multi{}, files: map[string]string{}, creds: map[string]*Credential{}}
	for _, p := range cmd.Params {
		m := &multi{}
		g.params[p.Flag] = m
		help := fmt.Sprintf("%s parameter %q", p.In, p.Name)
		switch {
		case p.Kind == List:
			help += ", repeated or comma-separated"
		case p.Kind == JSON:
			help += ", as JSON"
		}
		if p.Required {
			help += " (required)"
		}
		if p.Help != "" {
			help += ": " + p.Help
		}
		fs.Var(m, p.Flag, help)
	}
	if len(cmd.Media) > 0 {
		var kinds []string
		for _, m := range cmd.Media {
			kinds = append(kinds, m.MediaType)
		}
		help := "the request body: JSON (or text) as it is, @file, or - for stdin; a stream is sent as it is read"
		if cmd.BodyRequired {
			help += " (required)"
		}
		fs.StringVar(&g.body, "body", "", help)
		if len(cmd.Media) > 1 {
			fs.StringVar(&g.contentType, "content-type", cmd.Media[0].MediaType, "the media type of the body: "+strings.Join(kinds, ", "))
		}
		if slices.ContainsFunc(cmd.Media, func(m Media) bool { return m.Kind == "parts" || m.Kind == "partstream" }) {
			fs.Func("part", "a part of the body, in order: @file, - for stdin, or the text itself (repeatable); without it, -body is a multipart document", func(v string) error {
				g.parts = append(g.parts, partArg{source: v, header: textproto.MIMEHeader{}})
				return nil
			})
			fs.Func("part-header", "a header of the -part before it, name:value (repeatable)", func(v string) error {
				if len(g.parts) == 0 {
					return errors.New("give -part first")
				}
				name, value, ok := strings.Cut(v, ":")
				if !ok {
					return fmt.Errorf("%q is not name:value", v)
				}
				g.parts[len(g.parts)-1].header.Add(strings.TrimSpace(name), strings.TrimSpace(value))
				return nil
			})
		}
		seen := map[string]bool{}
		for _, m := range cmd.Media {
			for _, f := range m.Files {
				if seen[f.Flag] {
					continue
				}
				seen[f.Flag] = true
				flagName := f.Flag
				fs.Func(flagName, fmt.Sprintf("the file of the multipart property %q, as @file", f.Property), func(v string) error {
					g.files[flagName] = v
					return nil
				})
			}
		}
	}
	fs.StringVar(&g.server, "server", "", fmt.Sprintf("the server URL (or %s; default %s)", app.env("SERVER"), app.Server))
	fs.Var(&g.headers, "header", "a header to send, name:value (repeatable)")
	fs.DurationVar(&g.timeout, "timeout", 0, "how long the call may take (0: no limit)")
	fs.BoolVar(&g.verbose, "v", false, "print the status and headers of the response on stderr")
	for _, s := range app.Schemes {
		c := &Credential{}
		g.creds[s.Flag] = c
		if s.Basic {
			fs.StringVar(&c.User, s.Flag+"-user", "", fmt.Sprintf("the user name of %s (or %s_USER)", s.Help, app.env(s.Env)))
			fs.StringVar(&c.Password, s.Flag+"-password", "", fmt.Sprintf("the password of %s (or %s_PASSWORD)", s.Help, app.env(s.Env)))
		} else {
			fs.StringVar(&c.Value, s.Flag, "", fmt.Sprintf("%s (or %s)", s.Help, app.env(s.Env)))
		}
	}
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s %s [flags]\n\n%s\n\nFlags:\n", app.Name, cmd.Name, cmd.Summary)
		fs.PrintDefaults()
	}
	return fs, g
}

// credentials returns the credentials given, by flag or environment.
func (app App[C]) credentials(g *given) map[string]Credential {
	out := map[string]Credential{}
	for _, s := range app.Schemes {
		c := *g.creds[s.Flag]
		if s.Basic {
			if c.User == "" {
				c.User = os.Getenv(app.env(s.Env) + "_USER")
			}
			if c.Password == "" {
				c.Password = os.Getenv(app.env(s.Env) + "_PASSWORD")
			}
			if c.User != "" {
				out[s.Flag] = c
			}
			continue
		}
		if c.Value == "" {
			c.Value = os.Getenv(app.env(s.Env))
		}
		if c.Value != "" {
			out[s.Flag] = c
		}
	}
	return out
}
