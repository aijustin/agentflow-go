package toolcatalog

import "fmt"

type toolNotFoundError struct {
	name string
}

func errToolNotFound(name string) error {
	return toolNotFoundError{name: name}
}

func (e toolNotFoundError) Error() string {
	return fmt.Sprintf("toolcatalog: tool %q not found", e.name)
}
