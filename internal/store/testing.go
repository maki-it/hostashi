// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "context"

// RawExec and RawCount exist so tests in other packages can prepare and
// inspect state without a method for every case. They run fixed SQL given by
// the test, never user input.

// RawExec runs one statement.
func (s *Store) RawExec(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, query, args...)

	return err
}

// RawCount runs a query that returns one integer.
func (s *Store) RawCount(ctx context.Context, query string, arg any, out *int) error {
	return s.db.QueryRowContext(ctx, query, arg).Scan(out)
}
