package tokenauth

import "context"

// IdentityCredential is implemented by provider-specific typed credential
// inputs. The core never assumes password, SMS, Apple, or another protocol.
type IdentityCredential interface {
	CredentialKind() string
}

// ExternalIdentity is the stable provider/subject result of authentication.
// IdentityAuthenticator can resolve it through an application-owned
// IdentityLookup without coupling the provider to local user models.
type ExternalIdentity struct {
	Provider   string
	Subject    string
	Attributes map[string]string
}

// IdentityProvider is the extension point for project-owned authentication
// adapters. Implementations must not return raw credentials in Attributes;
// IdentityAuthenticator rejects common credential-bearing attribute names.
type IdentityProvider interface {
	Name() string
	Authenticate(context.Context, IdentityCredential) (ExternalIdentity, error)
}
