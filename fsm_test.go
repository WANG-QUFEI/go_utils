package fsm_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	fsm "github.com/WANG-QUFEI/go_fsm"
	"github.com/google/uuid"
	"github.com/hashicorp/go-memdb"
	"github.com/stretchr/testify/require"
)

type PaymentStatus string

const (
	Uninitiated PaymentStatus = "uninitiated"
	Pending     PaymentStatus = "pending"
	Completed   PaymentStatus = "completed"
	Failed      PaymentStatus = "failed"
	Cancelled   PaymentStatus = "cancelled"

	BankPaymentCreated   = "payment_created"
	BankPaymentCompleted = "payment_completed"
	BankPaymentFailed    = "payment_failed"
	BankPaymentCancelled = "payment_cancelled"
)

type BankPayment struct {
	UserId      string
	PaymentId   string
	AmountCents float64
	Currency    string
	Status      PaymentStatus
}

type PaymentEvent struct {
	EventId           string
	PaymentId         string
	PayerId           string
	AmountCents       float64
	Currency          string
	PayerBankAccount  string
	PayeeBankAccount  string
	BankPaymentStatus string
}

func (pe PaymentEvent) Id() string {
	return pe.EventId
}

func (bp *BankPayment) Transition(event PaymentEvent) (fsm.State[PaymentEvent], error) {
	if err := validateEvent(bp, &event); err != nil {
		return nil, fmt.Errorf("event validation failed: %w", err)
	}

	switch event.BankPaymentStatus {
	case BankPaymentCreated:
		if bp.Status == Uninitiated {
			bp.Status = Pending
		} else {
			return nil, fmt.Errorf("invalid transition from %s to pending", bp.Status)
		}

	case BankPaymentCompleted:
		if bp.Status == Pending {
			bp.Status = Completed
		} else {
			return nil, fmt.Errorf("invalid transition from %s to %s", bp.Status, event.BankPaymentStatus)
		}

	case BankPaymentFailed:
		if bp.Status == Pending {
			bp.Status = Failed
		} else {
			return nil, fmt.Errorf("invalid transition from %s to %s", bp.Status, event.BankPaymentStatus)
		}

	case BankPaymentCancelled:
		if bp.Status == Pending {
			bp.Status = Cancelled
		} else {
			return nil, fmt.Errorf("invalid transition from %s to %s", bp.Status, event.BankPaymentStatus)
		}
	default:
		return nil, fmt.Errorf("unknown bank payment status: %s", event.BankPaymentStatus)
	}

	return bp, nil
}

func (bp *BankPayment) String() string {
	return fmt.Sprintf("BankPayment{UserId: %s, PaymentId: %s, AmountCents: %.2f, Currency: %s, Status: %s}",
		bp.UserId, bp.PaymentId, bp.AmountCents, bp.Currency, bp.Status)
}

type TestEngine struct {
	timeout time.Duration
	ch      chan PaymentEvent
	db      *memdb.MemDB
}

func (e *TestEngine) Receive(ctx context.Context) (PaymentEvent, error) {
	select {
	case <-ctx.Done():
		return PaymentEvent{}, ctx.Err()
	case event, ok := <-e.ch:
		if ok {
			return event, nil
		} else {
			return PaymentEvent{}, fmt.Errorf("channel closed")
		}
	case <-time.After(e.timeout):
		return PaymentEvent{}, fmt.Errorf("timeout receiving event")
	}
}

func (e *TestEngine) RestoreState(_ context.Context, pe PaymentEvent) (fsm.State[PaymentEvent], error) {
	if pe.PaymentId == "" {
		return nil, fmt.Errorf("payment id cannot be empty")
	}

	tx := e.db.Txn(false)
	defer tx.Abort()

	bpRaw, err := tx.First("bank_payments", "id", pe.PaymentId)
	if err != nil {
		return nil, fmt.Errorf("failed to get payment by id '%s', error: %w", pe.PaymentId, err)
	}
	if bpRaw == nil {
		return nil, fmt.Errorf("payment with id '%s' not found", pe.PaymentId)
	}

	bp, ok := bpRaw.(*BankPayment)
	if !ok {
		return nil, fmt.Errorf("expected BankPayment type, got %T", bpRaw)
	}
	return bp, nil
}

func (e *TestEngine) SaveState(_ context.Context, state fsm.State[PaymentEvent]) error {
	bp, ok := state.(*BankPayment)
	if !ok {
		return fmt.Errorf("unexpected type of state %T", state)
	}

	tx := e.db.Txn(true)
	defer tx.Abort()
	if err := tx.Insert("bank_payments", bp); err != nil {
		return err
	}

	tx.Commit()
	return nil
}

func TestFSM(t *testing.T) {
	db, err := initializeDB()
	if err != nil {
		t.Fatal(err)
	}

	payments := []BankPayment{
		{
			UserId:      uuid.NewString(),
			PaymentId:   uuid.NewString(),
			AmountCents: 100,
			Currency:    "EUR",
			Status:      Uninitiated,
		},
		{
			UserId:      uuid.NewString(),
			PaymentId:   uuid.NewString(),
			AmountCents: 200,
			Currency:    "EUR",
			Status:      Uninitiated,
		},
		{
			UserId:      uuid.NewString(),
			PaymentId:   uuid.NewString(),
			AmountCents: 300,
			Currency:    "EUR",
			Status:      Uninitiated,
		},
	}

	tx := db.Txn(true)
	for _, payment := range payments {
		if err := tx.Insert("bank_payments", &payment); err != nil {
			t.Fatal(err)
		}
	}
	tx.Commit()

	events := []PaymentEvent{
		{
			EventId:           uuid.NewString(),
			PaymentId:         payments[0].PaymentId,
			PayerId:           payments[0].UserId,
			AmountCents:       payments[0].AmountCents,
			Currency:          payments[0].Currency,
			PayerBankAccount:  "123",
			PayeeBankAccount:  "456",
			BankPaymentStatus: BankPaymentCreated,
		},
		{
			EventId:           uuid.NewString(),
			PaymentId:         payments[1].PaymentId,
			PayerId:           payments[1].UserId,
			AmountCents:       payments[1].AmountCents,
			Currency:          payments[1].Currency,
			PayerBankAccount:  "123",
			PayeeBankAccount:  "456",
			BankPaymentStatus: BankPaymentCreated,
		},
		{
			EventId:           uuid.NewString(),
			PaymentId:         payments[2].PaymentId,
			PayerId:           payments[2].UserId,
			AmountCents:       payments[2].AmountCents,
			Currency:          payments[2].Currency,
			PayerBankAccount:  "123",
			PayeeBankAccount:  "456",
			BankPaymentStatus: BankPaymentCreated,
		},
		{
			EventId:           uuid.NewString(),
			PaymentId:         payments[0].PaymentId,
			PayerId:           payments[0].UserId,
			AmountCents:       payments[0].AmountCents,
			Currency:          payments[0].Currency,
			PayerBankAccount:  "123",
			PayeeBankAccount:  "456",
			BankPaymentStatus: BankPaymentCompleted,
		},
		{
			EventId:           uuid.NewString(),
			PaymentId:         payments[1].PaymentId,
			PayerId:           payments[1].UserId,
			AmountCents:       payments[1].AmountCents,
			Currency:          payments[1].Currency,
			PayerBankAccount:  "123",
			PayeeBankAccount:  "456",
			BankPaymentStatus: BankPaymentFailed,
		},
		{
			EventId:           uuid.NewString(),
			PaymentId:         payments[2].PaymentId,
			PayerId:           payments[2].UserId,
			AmountCents:       payments[2].AmountCents,
			Currency:          payments[2].Currency,
			PayerBankAccount:  "123",
			PayeeBankAccount:  "456",
			BankPaymentStatus: BankPaymentCancelled,
		},
	}

	ch := make(chan PaymentEvent)
	chDone := make(chan struct{})

	go func() {
		for _, event := range events {
			ch <- event
			time.Sleep(100 * time.Millisecond) // Simulate some delay between events
		}
		chDone <- struct{}{} // Signal that all events have been sent
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-chDone
		cancel() // Cancel the context when all events are sent
	}()

	te := &TestEngine{
		timeout: time.Hour,
		ch:      ch,
		db:      db,
	}
	err = fsm.StartExecution(ctx, te)
	require.ErrorIs(t, err, context.Canceled, "expected context cancellation error")

	for i := range payments {
		tx := db.Txn(false)
		bpRaw, err := tx.First("bank_payments", "id", payments[i].PaymentId)
		require.NoError(t, err, "failed to get payment by id")
		require.NotNil(t, bpRaw, "payment should exist in the database")

		bp, ok := bpRaw.(*BankPayment)
		require.True(t, ok, "expected BankPayment type")

		if i == 0 {
			require.Equal(t, Completed, bp.Status, "status should be completed for first payment")
		} else if i == 1 {
			require.Equal(t, Failed, bp.Status, "status should be failed for second payment")
		} else if i == 2 {
			require.Equal(t, Cancelled, bp.Status, "status should be cancelled for third payment")
		}

		tx.Abort()
	}
}

func initializeDB() (*memdb.MemDB, error) {
	schema := &memdb.DBSchema{
		Tables: map[string]*memdb.TableSchema{
			"bank_payments": {
				Name: "bank_payments",
				Indexes: map[string]*memdb.IndexSchema{
					"id": {
						Name:    "id",
						Unique:  true,
						Indexer: &memdb.StringFieldIndex{Field: "PaymentId"},
					},
					"user_id": {
						Name:    "user_id",
						Unique:  false,
						Indexer: &memdb.StringFieldIndex{Field: "UserId"},
					},
				},
			},
		},
	}

	return memdb.NewMemDB(schema)
}

func validateEvent(bp *BankPayment, event *PaymentEvent) error {
	if event.EventId == "" {
		return fmt.Errorf("event ID cannot be empty")
	}
	if event.PayerBankAccount == "" {
		return fmt.Errorf("payer bank account cannot be empty")
	}
	if event.PayeeBankAccount == "" {
		return fmt.Errorf("payee bank account cannot be empty")
	}
	if event.BankPaymentStatus == "" {
		return fmt.Errorf("bank response code cannot be empty")
	}
	if bp.PaymentId != event.PaymentId {
		return fmt.Errorf("payment ID mismatch: %s != %s", bp.PaymentId, event.PaymentId)
	}
	if bp.UserId != event.PayerId {
		return fmt.Errorf("payer ID mismatch: %s != %s", bp.UserId, event.PayerId)
	}
	if bp.AmountCents != event.AmountCents {
		return fmt.Errorf("amount mismatch: %f != %f", bp.AmountCents, event.AmountCents)
	}
	if bp.Currency != event.Currency {
		return fmt.Errorf("currency mismatch: %s != %s", bp.Currency, event.Currency)
	}

	return nil
}
