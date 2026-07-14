package fizeau

import "github.com/easel/fizeau/internal/serviceimpl"

// newSessionHub keeps white-box fixture construction compact while production
// code constructs the internal hub directly at the public facade boundary.
func newSessionHub() *serviceimpl.SessionHub {
	return serviceimpl.NewSessionHub()
}
