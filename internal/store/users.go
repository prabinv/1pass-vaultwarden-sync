package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// User represents an authenticated user account.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

func (s *UserStore) Create(ctx context.Context, email, passwordHash string) (*User, error) {
	var u User
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash)
		 VALUES ($1, $2)
		 RETURNING id, email, password_hash, created_at`,
		email, passwordHash,
	)
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	row := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = $1`,
		email,
	)
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}
