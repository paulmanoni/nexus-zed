// Package users is a demo file for the Nexus Zed extension. Open it in Zed (with
// the extension installed) to see decorator detection in action: the valid
// decorators get an outline entry + hover, and the three marked mistakes below
// raise diagnostics.
//
// It is self-contained (stub types, no imports) so the ONLY squiggles you see
// are the nexus decorator diagnostics — not Go build errors. In a real nexus
// app these handlers take *nexus.App, nexus.Params[T], *nexus.WSSession, etc.
package users

type (
	Service    struct{}
	User       struct{}
	GetArgs    struct{}
	SearchArgs struct{}
	Event      struct{}
)

//@provide
func NewService() *Service {
	return &Service{}
}

//@rest GET /users/:id
func NewGetUser(s *Service, p GetArgs) (*User, error) {
	return nil, nil
}

//@query
//@auth Requires("ADMIN")
func NewSearchUsers(s *Service, p SearchArgs) ([]User, error) {
	return nil, nil
}

//@ws /events user.created
func NewUserCreated(s *Service, p Event) error {
	return nil
}

// --- intentional mistakes: each raises a nexus diagnostic ---

//@reset GET /typo
func NewTypo(s *Service) (*User, error) { return nil, nil } // unknown decorator → "did you mean //@rest?"

//@rest GET
func NewMissingPath(s *Service) (*User, error) { return nil, nil } // //@rest needs METHOD and PATH

//@auth Required
func NewOrphanAuth(s *Service) (*User, error) { return nil, nil } // modifier with no primary
