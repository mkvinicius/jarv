// Package cli implements the channel.Channel interface for terminal (stdin/stdout).
//
// The CLI channel reads lines from stdin and writes responses to stdout.
// It supports streaming output (token-by-token) and multi-line input with
// a simple ">>>" prompt style.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mkvinicius/jarv/internal/ports/channel"
)

// Channel implements channel.Channel for the terminal.
type Channel struct {
	id      string
	scanner *bufio.Scanner
	mu      sync.Mutex
	stopped bool
	prompt  string
}

// New creates a CLI channel with the given prompt string.
func New(prompt string) *Channel {
	if prompt == "" {
		prompt = "você"
	}
	return &Channel{
		id:      "cli-" + uuid.NewString()[:8],
		scanner: bufio.NewScanner(os.Stdin),
		prompt:  prompt,
	}
}

func (c *Channel) ID() string   { return c.id }
func (c *Channel) Name() string { return "cli" }
func (c *Channel) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.stopped
}

// Start begins reading lines from stdin and calling handler for each message.
// Blocks until ctx is cancelled, EOF, or Stop() is called.
func (c *Channel) Start(ctx context.Context, handler func(channel.InboundMessage)) error {
	sessionID := uuid.NewString()
	userID := "local-user"

	fmt.Fprintln(os.Stdout, "JARV iniciado. Digite sua mensagem (Ctrl+C para sair).")
	fmt.Fprintln(os.Stdout, strings.Repeat("─", 50))

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		c.mu.Lock()
		if c.stopped {
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()

		fmt.Fprintf(os.Stdout, "\n%s: ", c.prompt)

		if !c.scanner.Scan() {
			return nil // EOF
		}

		text := strings.TrimSpace(c.scanner.Text())
		if text == "" {
			continue
		}
		if text == "/sair" || text == "/exit" || text == "/quit" {
			fmt.Fprintln(os.Stdout, "Até mais!")
			return nil
		}

		msg := channel.InboundMessage{
			ID:         uuid.NewString(),
			ChannelID:  c.id,
			SessionID:  sessionID,
			Sender:     channel.Sender{ID: userID, DisplayName: c.prompt},
			Text:       text,
			ReceivedAt: time.Now(),
		}

		handler(msg)
	}
}

// Send writes a message to stdout.
func (c *Channel) Send(_ context.Context, msg channel.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return fmt.Errorf("cli: channel stopped")
	}
	fmt.Fprintf(os.Stdout, "\nJARV: %s\n", msg.Text)
	return nil
}

// Stop signals the channel to stop reading.
func (c *Channel) Stop() error {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	return nil
}

// PrintStream writes streaming tokens to stdout as they arrive.
// Used for real-time token-by-token output.
func PrintStream(tokenCh <-chan string) {
	fmt.Fprintf(os.Stdout, "\nJARV: ")
	for token := range tokenCh {
		fmt.Fprint(os.Stdout, token)
	}
	fmt.Fprintln(os.Stdout)
}
