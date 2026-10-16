// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type storedReceipt struct {
	hash    string
	receipt contract.Receipt
}

func writeReceipt(ctx context.Context, tx *sql.Tx, op, hash string, rev contract.Revision, payload json.RawMessage) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO tables_schema_receipts(operation_id,request_hash,schema_revision,data_revision,payload,applied_at) VALUES(?,?,?,?,?,?)",
		op, hash, rev.Schema, rev.Data, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readReceipt(ctx context.Context, q rowQuery, op string) (storedReceipt, bool, error) {
	var r storedReceipt
	var payload string
	var rev contract.Revision
	err := q.QueryRowContext(ctx, "SELECT request_hash,schema_revision,data_revision,payload FROM tables_schema_receipts WHERE operation_id=?", op).
		Scan(&r.hash, &rev.Schema, &rev.Data, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.receipt = contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: json.RawMessage(payload)}}
	return r, true, nil
}

// LookupReceipt returns what is durably known about an operation, schema
// operations included. A schema change whose reply was lost is found in its
// database even when the ledger line was never finished. An operation that
// began but left no receipt in its database did not commit; it reports
// unknown, and the same ID may still be applied.
func (s *Store) LookupReceipt(ctx context.Context, operationID string) (contract.Receipt, error) {
	if err := validOperation(operationID); err != nil {
		return contract.Receipt{}, err
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	op, found, err := s.lookup(ctx, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if !found {
		return contract.Receipt{OperationID: operationID, State: contract.ReceiptUnknown}, nil
	}
	if op.Kind != "schema" && op.Kind != "rows" {
		return op.receipt(), nil
	}
	db, err := s.openDatabase(op.Resource)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return contract.Receipt{OperationID: operationID, State: contract.ReceiptUnknown}, nil
		}
		return contract.Receipt{}, err
	}
	defer func() { _ = db.Close() }()
	if ok, e := hasReceipts(ctx, db); e != nil {
		return contract.Receipt{}, e
	} else if !ok {
		return contract.Receipt{OperationID: operationID, State: contract.ReceiptUnknown}, nil
	}
	r, ok, err := readReceipt(ctx, db, operationID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if !ok {
		return contract.Receipt{OperationID: operationID, State: contract.ReceiptUnknown}, nil
	}
	if op.State != "committed" {
		if err = s.finish(ctx, operationID, r.receipt.Revision); err != nil {
			return contract.Receipt{}, err
		}
	}
	return r.receipt, nil
}

func hasReceipts(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tables_schema_receipts'").Scan(&n)
	return n == 1, err
}
