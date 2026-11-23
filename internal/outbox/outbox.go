// Package outbox implements the transactional outbox contract.
//
// State changes and outbox rows are committed in one transaction (the caller's). A publisher then claims committed
// rows with FOR UPDATE SKIP LOCKED, publishes them, and marks them. This package intentionally does not know about
// NATS: Phase 05 supplies a Publisher implementation. The deliverable here is the durable table plus the contract.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agent-platform/agent-platform/internal/platform/postgres"
)

// Message is one row of execution.outbox plus the publisher-facing fields.
type Message struct {
	ID            string
	MessageID     string
	Subject       string
	MessageType   string
	SchemaVersion int
	OrgID         string
	ExecutionID   *string
	Payload       json.RawMessage
	Headers       json.RawMessage
	AvailableAt   time.Time
	Attempts      int
}

// Publisher delivers one message. It must honour MessageID as the broker idempotency key when the broker supports
// one; the outbox is at-least-once regardless.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// Store reads and writes the outbox table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a store bound to the orchestrator's pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// NewMessage builds a committed-ready message with a UUIDv7 row ID and a stable prefixed message ID.
func NewMessage(orgID, subject, messageType string, executionID *string, payload any, headers map[string]string) (Message, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("outbox: marshal payload: %w", err)
	}
	if !json.Valid(raw) || len(raw) == 0 || raw[0] != '{' {
		return Message{}, errors.New("outbox: payload must be a JSON object")
	}
	if headers == nil {
		headers = map[string]string{}
	}
	rawHeaders, err := json.Marshal(headers)
	if err != nil {
		return Message{}, fmt.Errorf("outbox: marshal headers: %w", err)
	}

	rowID, err := uuid.NewV7()
	if err != nil {
		return Message{}, err
	}
	messageUUID, err := uuid.NewV7()
	if err != nil {
		return Message{}, err
	}

	msg := Message{
		ID:            rowID.String(),
		MessageID:     "msg_" + messageUUID.String(),
		Subject:       subject,
		MessageType:   messageType,
		SchemaVersion: 1,
		OrgID:         orgID,
		ExecutionID:   executionID,
		Payload:       raw,
		Headers:       rawHeaders,
		AvailableAt:   time.Now().UTC(),
	}
	if err := msg.Validate(); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// Validate checks the fields the table also checks, so a programming error fails before the transaction.
func (m Message) Validate() error {
	if m.ID == "" {
		return errors.New("outbox: id is required")
	}
	if _, err := uuid.Parse(m.ID); err != nil {
		return fmt.Errorf("outbox: id must be a UUID: %w", err)
	}
	if len(m.MessageID) < 12 || m.MessageID[:4] != "msg_" {
		return fmt.Errorf("outbox: message_id %q must start with msg_", m.MessageID)
	}
	if m.Subject == "" || len(m.Subject) > 255 {
		return errors.New("outbox: subject must be 1..255 characters")
	}
	if m.MessageType == "" || len(m.MessageType) > 128 {
		return errors.New("outbox: message_type must be 1..128 characters")
	}
	if m.SchemaVersion < 1 {
		return errors.New("outbox: schema_version must be at least 1")
	}
	if m.OrgID == "" {
		return errors.New("outbox: org_id is required")
	}
	if len(m.Payload) == 0 || !json.Valid(m.Payload) {
		return errors.New("outbox: payload must be valid JSON")
	}
	if len(m.Headers) == 0 || !json.Valid(m.Headers) {
		return errors.New("outbox: headers must be valid JSON")
	}
	return nil
}

// Insert writes one message inside the caller's transaction.
func (s *Store) Insert(ctx context.Context, tx pgx.Tx, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO execution.outbox
			(id, message_id, subject, message_type, schema_version, org_id, execution_id,
			 payload, headers, available_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		msg.ID, msg.MessageID, msg.Subject, msg.MessageType, msg.SchemaVersion,
		msg.OrgID, msg.ExecutionID, msg.Payload, msg.Headers, msg.AvailableAt,
	)
	if err != nil {
		return fmt.Errorf("outbox: insert: %w", err)
	}
	return nil
}

// Claim locks up to limit due rows, skipping rows held by another publisher, and returns them. The rows remain
// locked until the caller commits or rolls back.
func (s *Store) Claim(ctx context.Context, tx pgx.Tx, limit int) ([]Message, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("outbox: limit must be between 1 and 100")
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, message_id, subject, message_type, schema_version, org_id, execution_id,
		       payload, headers, available_at, attempts
		FROM execution.outbox
		WHERE published_at IS NULL AND available_at <= now()
		ORDER BY available_at, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("outbox: claim: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var executionID pgtype.Text
		if err := rows.Scan(
			&msg.ID, &msg.MessageID, &msg.Subject, &msg.MessageType, &msg.SchemaVersion,
			&msg.OrgID, &executionID, &msg.Payload, &msg.Headers, &msg.AvailableAt, &msg.Attempts,
		); err != nil {
			return nil, fmt.Errorf("outbox: scan: %w", err)
		}
		if executionID.Valid {
			msg.ExecutionID = &executionID.String
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: rows: %w", err)
	}
	return messages, nil
}

// MarkPublished records a successful publish in the caller's transaction.
func (s *Store) MarkPublished(ctx context.Context, tx pgx.Tx, id string) error {
	tag, err := tx.Exec(ctx,
		`UPDATE execution.outbox SET published_at = now(), last_error = NULL WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("outbox: mark published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("outbox: row %s not found", id)
	}
	return nil
}

// MarkFailed records a failed publish and schedules a retry.
func (s *Store) MarkFailed(ctx context.Context, tx pgx.Tx, id string, retryAfter time.Duration, reason string) error {
	if retryAfter < 0 {
		retryAfter = 0
	}
	tag, err := tx.Exec(ctx, `
		UPDATE execution.outbox
		SET attempts = attempts + 1,
		    last_error = $2,
		    available_at = now() + ($3::bigint * interval '1 millisecond')
		WHERE id = $1::uuid`, id, reason, retryAfter.Milliseconds())
	if err != nil {
		return fmt.Errorf("outbox: mark failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("outbox: row %s not found", id)
	}
	return nil
}

// DispatchOnce claims and publishes one batch. Rows are marked published or rescheduled in the same transaction
// that claimed them. A crash after publish and before commit may duplicate a message; consumers are idempotent by
// contract, and MessageID is the Nats-Msg-Id.
func (s *Store) DispatchOnce(ctx context.Context, publisher Publisher, limit int, retryAfter time.Duration) (int, error) {
	if publisher == nil {
		return 0, errors.New("outbox: publisher is required")
	}
	published := 0
	var errs []error
	err := postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		messages, err := s.Claim(ctx, tx, limit)
		if err != nil {
			return err
		}
		for _, msg := range messages {
			if err := publisher.Publish(ctx, msg); err != nil {
				if markErr := s.MarkFailed(ctx, tx, msg.ID, retryAfter, err.Error()); markErr != nil {
					errs = append(errs, markErr)
				}
				errs = append(errs, fmt.Errorf("outbox: publish %s: %w", msg.MessageID, err))
				continue
			}
			if err := s.MarkPublished(ctx, tx, msg.ID); err != nil {
				errs = append(errs, err)
				continue
			}
			published++
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return published, errors.Join(errs...)
}
