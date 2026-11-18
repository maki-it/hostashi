package reconcile

import "context"

// Files is what a driver renders for one generation. Content depends only on
// the desired state (it is hashed for idempotency). Meta holds per-generation
// glue (for example the generation number) and is excluded from that hash.
type Files struct {
	Content map[string][]byte
	Meta    map[string][]byte
}

// Driver is the spike version of the WebServerDriver from 04-architecture.md.
type Driver interface {
	// Render turns desired state into files, relative to the generation dir.
	Render(d Desired, gen int) (Files, error)
	// Validate checks a complete generation directory without activating it.
	Validate(ctx context.Context, genDir string) error
	// Activate makes the server use currentDir (start it or reload it).
	Activate(ctx context.Context, currentDir string) error
	// Verify proves that generation gen is live and the sites answer.
	Verify(ctx context.Context, gen int, d Desired) error
}
