// Package composition owns the one module definition consumed by every
// shipped Aginex process. Derived applications should provide an equivalent
// package that calls application.Define with their compiled-in modules.
package composition

import "github.com/xgtian-root/aginex/backend/framework/application"

var definition = mustDefine()

// Definition returns the immutable composition root for this distribution.
func Definition() application.Definition {
	return definition
}

func mustDefine() application.Definition {
	result, err := application.Define(
		application.FilesModule(),
		application.StarterExampleModule(),
	)
	if err != nil {
		panic(err)
	}
	return result
}
