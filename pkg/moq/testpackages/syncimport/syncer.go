package syncimport

import (
	stdsync "sync"

	"github.com/liruohrh/moq/pkg/moq/testpackages/syncimport/sync"
)

// Syncer is a test interface.
type Syncer interface {
	Blah(s sync.Thing, wg *stdsync.WaitGroup)
}
