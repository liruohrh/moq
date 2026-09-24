package typealias

import (
	"github.com/liruohrh/moq/pkg/moq/testpackages/typealiastwo"
)

type Example interface {
	Do(a typealiastwo.AliasType, b typealiastwo.GenericAliasType) error
}
