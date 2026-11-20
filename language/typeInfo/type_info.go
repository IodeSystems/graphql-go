package typeInfo

import (
	"github.com/IodeSystems/graphql-go/v3/language/ast"
)

// TypeInfoI defines the interface for TypeInfo Implementation
type TypeInfoI interface {
	Enter(node ast.Node)
	Leave(node ast.Node)
}
