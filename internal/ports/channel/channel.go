// Package channel defines the communication channel port for JARV.
//
// A Channel is any interface through which a user can interact with JARV:
// the web dashboard, CLI, WhatsApp, Telegram, etc.
//
// The core agent never knows which channel is active — it only processes
// InboundMessage and produces OutboundMessage. The adapters handle
// the translation to/from platform-specific formats.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package channel

import (
	"context"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Message Types
// ─────────────────────────────────────────────────────────────────────────────

// MediaKind classifies attached media in a message.
type MediaKind string

const (
	MediaImage    MediaKind = "image"
	MediaAudio    MediaKind = "audio"
	MediaVideo    MediaKind = "video"
	MediaDocument MediaKind = "document"
)

// Media represents an attached file or media item.
type Media struct {
	Kind     MediaKind `json:"kind"`
	URL      string    `json:"url,omitempty"`   // remote URL
	Data     []byte    `json:"data,omitempty"`  // raw bytes (for small files)
	MimeType string    `json:"mime_type"`
	Filename string    `json:"filename,omitempty"`
	Size     int64     `json:"size,omitempty"`
}

// Sender identifies the user who sent a message.
type Sender struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	IsBot       bool   `json:"is_bot"`
	Locale      string `json:"locale,omitempty"` // e.g., "pt-BR"
}

// InboundMessage is a message received from any channel.
type InboundMessage struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"`
	SessionID string    `json:"session_id"`
	Sender    Sender    `json:"sender"`
	Text      string    `json:"text"`
	Media     []Media   `json:"media,omitempty"`
	ReplyToID string    `json:"reply_to_id,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
}

// OutboundMessage is a message to be sent to a channel.
type OutboundMessage struct {
	ChannelID  string   `json:"channel_id"`
	SessionID  string   `json:"session_id"`
	RecipientID string  `json:"recipient_id"`
	Text       string   `json:"text"`
	Media      []Media  `json:"media,omitempty"`
	ReplyToID  string   `json:"reply_to_id,omitempty"`
	Buttons    []Button `json:"buttons,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Button represents an interactive button in a message (for WhatsApp, Telegram, etc.).
type Button struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"` // for link buttons
}

// ─────────────────────────────────────────────────────────────────────────────
// Channel Interface
// ─────────────────────────────────────────────────────────────────────────────

// Channel is the interface for all communication adapters in JARV.
type Channel interface {
	// ID returns the unique identifier for this channel instance.
	ID() string

	// Name returns the human-readable channel name (e.g., "whatsapp", "web", "cli").
	Name() string

	// Start begins listening for inbound messages.
	// Messages are delivered via the provided handler function.
	Start(ctx context.Context, handler func(InboundMessage)) error

	// Send delivers an outbound message to the channel.
	Send(ctx context.Context, msg OutboundMessage) error

	// Stop gracefully shuts down the channel.
	Stop() error

	// Healthy checks if the channel connection is active.
	Healthy() bool
}

// ─────────────────────────────────────────────────────────────────────────────
// Channel Manager — Multi-Channel Orchestration
// ─────────────────────────────────────────────────────────────────────────────

// Manager orchestrates multiple channels, routing messages to the agent
// and delivering responses back to the correct channel.
type Manager interface {
	// Register adds a channel to the manager.
	Register(ch Channel) error

	// Start begins all registered channels.
	Start(ctx context.Context, handler func(InboundMessage)) error

	// Send routes an outbound message to the correct channel.
	Send(ctx context.Context, msg OutboundMessage) error

	// Stop gracefully shuts down all channels.
	Stop() error

	// Channels returns the list of registered channel IDs.
	Channels() []string
}
