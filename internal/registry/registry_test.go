package registry_test

import (
	"testing"

	"github.com/liruohrh/moq/internal/registry"
)

func BenchmarkLoadSource(b *testing.B) {
	for i := 0; i < b.N; i++ {
		registry.LoadSource("../../pkg/moq/testpackages/example", "")
	}
}
