package source

const (
	name = "GraphQL"
)

// Source represents a GraphQL document for parsing.
//
// Body must not be mutated after Parse: token values (identifiers, numbers)
// are zero-copy views into Body for performance.
type Source struct {
	Body []byte
	Name string
}

func NewSource(s *Source) *Source {
	if s == nil {
		s = &Source{Name: name}
	}
	if s.Name == "" {
		s.Name = name
	}
	return s
}
