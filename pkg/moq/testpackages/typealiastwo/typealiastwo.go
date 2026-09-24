package typealiastwo

import "github.com/liruohrh/moq/pkg/moq/testpackages/typealiastwo/internal/typealiasinternal"

type AliasType = typealiasinternal.MyInternalType

type GenericAliasType = typealiasinternal.MyGenericType[int]
