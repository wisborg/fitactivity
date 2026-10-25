package strava

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LoginOptions are how a login reaches the athlete.
type LoginOptions struct {
	// Open shows the athlete a page: their browser. Nil, or failing, and
	// the page's address is printed for them to open instead.
	Open func(url string) error
	// Out is where the athlete is told what to do.
	Out io.Writer
	// Paste, when not nil, is where the athlete pastes the address Strava
	// sent their browser back to, for a machine that cannot listen for it
	// -- one reached over ssh, whose browser is somewhere else.
	Paste io.Reader
	// Wait is how long to wait for the athlete; zero is five minutes.
	Wait time.Duration
}

// callbackPath is where on localhost Strava sends the browser back to.
const callbackPath = "/exchange_token"

// Login asks the athlete on Strava to allow the application in, and keeps
// the token it gets. The browser is sent back to a server this listens
// with on localhost, at a port of its own, for as long as it waits; the
// application's Authorization Callback Domain must be "localhost" for
// Strava to send it there.
func (c *Client) Login(ctx context.Context, o LoginOptions) (Token, error) {
	state, err := randomState()
	if err != nil {
		return Token{}, err
	}
	if o.Wait == 0 {
		o.Wait = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Wait)
	defer cancel()
	if o.Paste != nil {
		return c.loginByPaste(ctx, o, state)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, fmt.Errorf("listening for Strava's answer on localhost: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	listeners := []net.Listener{ln}
	// A browser may take localhost for ::1 first; listen there too where
	// the machine has it.
	if ln6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port)); err == nil {
		listeners = append(listeners, ln6)
	}
	type answer struct {
		q   url.Values
		err error
	}
	answers := make(chan answer, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		err := checkAnswer(q, state)
		page := "Logged in to Strava. You can close this tab and go back to the terminal."
		if err != nil {
			page = "Not logged in: " + err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>Strava</title><p>%s</p>", html.EscapeString(page))
		select {
		case answers <- answer{q, err}:
		default:
		}
	})}
	for _, l := range listeners {
		go srv.Serve(l)
	}
	defer srv.Close()

	redirect := fmt.Sprintf("http://localhost:%d%s", port, callbackPath)
	c.show(o, c.AuthURL(redirect, state))
	select {
	case a := <-answers:
		if a.err != nil {
			return Token{}, a.err
		}
		return c.Exchange(ctx, a.q.Get("code"), a.q.Get("scope"))
	case <-ctx.Done():
		return Token{}, fmt.Errorf("no answer from Strava within %v: %w", o.Wait, ctx.Err())
	}
}

// loginByPaste is a login whose answer the athlete pastes: Strava sends the
// browser to localhost, which on the browser's machine is nothing, and the
// address it could not open carries the code.
func (c *Client) loginByPaste(ctx context.Context, o LoginOptions, state string) (Token, error) {
	c.show(o, c.AuthURL("http://localhost"+callbackPath, state))
	fmt.Fprintln(o.Out, "Once you have allowed it, your browser will fail to open a page on localhost.")
	fmt.Fprint(o.Out, "Paste that page's whole address here: ")
	line, err := bufio.NewReader(o.Paste).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return Token{}, fmt.Errorf("reading the pasted address: %w", err)
	}
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return Token{}, fmt.Errorf("the pasted address: %w", err)
	}
	q := u.Query()
	if err := checkAnswer(q, state); err != nil {
		return Token{}, err
	}
	return c.Exchange(ctx, q.Get("code"), q.Get("scope"))
}

// show sends the athlete to page: their browser if it opens, and the address
// printed whether or not, for when it opens somewhere they are not looking.
func (c *Client) show(o LoginOptions, page string) {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	if o.Open != nil && o.Paste == nil && o.Open(page) == nil {
		fmt.Fprintf(out, "Opened Strava in your browser to allow access. If it did not appear, open:\n\n  %s\n\n", page)
		return
	}
	fmt.Fprintf(out, "Open this page to allow access to your Strava activities:\n\n  %s\n\n", page)
}

// checkAnswer is whether Strava's answer is a code for this login: the
// state it was sent with, and no refusal.
func checkAnswer(q url.Values, state string) error {
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return errors.New("access was not allowed on Strava")
		}
		return fmt.Errorf("Strava answered %q", e)
	}
	if q.Get("state") != state {
		return errors.New("the answer is not to this login: its state does not match")
	}
	if q.Get("code") == "" {
		return errors.New("the answer holds no code")
	}
	return nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
