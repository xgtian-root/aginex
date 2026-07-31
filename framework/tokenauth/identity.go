package tokenauth

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

const (
	maxProviderBytes          = 64
	maxIdentitySubjectBytes   = 320
	maxIdentityStatusBytes    = 64
	maxIdentityAttributes     = 32
	maxIdentityAttributeKey   = 64
	maxIdentityAttributeValue = 1024
)

// IdentityMapping is the application-local link for one provider/subject
// tuple. It deliberately carries no provider credential or credential hash.
type IdentityMapping struct {
	Provider string
	Subject  string
	UserID   string
	Status   string
	Active   bool
}

// IdentityLookup maps a provider-owned stable subject to an application user.
// Implementations must preserve the provider/subject tuple exactly and return
// ErrIdentityNotFound when no link exists.
type IdentityLookup interface {
	LookupIdentity(
		context.Context,
		string,
		string,
	) (IdentityMapping, error)
}

// IdentityAuthenticationDependencies keep provider authentication, identity
// linking, and application user status as three explicit trust boundaries.
type IdentityAuthenticationDependencies struct {
	Provider   IdentityProvider
	Identities IdentityLookup
	Subjects   SubjectLookup
}

// IdentityAuthenticator authenticates a provider credential and resolves the
// returned provider/subject tuple to one currently active local user.
type IdentityAuthenticator struct {
	provider     IdentityProvider
	providerName string
	identities   IdentityLookup
	subjects     SubjectLookup
}

// AuthenticatedIdentity is the transport-neutral result of provider
// authentication and local account resolution.
type AuthenticatedIdentity struct {
	External ExternalIdentity
	Mapping  IdentityMapping
	Subject  Subject
}

// NewIdentityAuthenticator constructs the provider-to-local-user bridge.
// Provider-specific credential exchange remains application-owned.
func NewIdentityAuthenticator(
	dependencies IdentityAuthenticationDependencies,
) (*IdentityAuthenticator, error) {
	if nilInterface(dependencies.Provider) {
		return nil, fmt.Errorf(
			"%w: identity provider is required",
			ErrInvalidConfig,
		)
	}
	if nilInterface(dependencies.Identities) {
		return nil, fmt.Errorf(
			"%w: identity lookup is required",
			ErrInvalidConfig,
		)
	}
	if nilInterface(dependencies.Subjects) {
		return nil, fmt.Errorf(
			"%w: subject lookup is required",
			ErrInvalidConfig,
		)
	}
	providerName := dependencies.Provider.Name()
	if !validProviderName(providerName) {
		return nil, fmt.Errorf(
			"%w: identity provider name is invalid",
			ErrInvalidConfig,
		)
	}
	return &IdentityAuthenticator{
		provider:     dependencies.Provider,
		providerName: providerName,
		identities:   dependencies.Identities,
		subjects:     dependencies.Subjects,
	}, nil
}

// Authenticate verifies a provider credential, requires an exact
// provider/subject mapping, and re-checks the current local user status.
func (authenticator *IdentityAuthenticator) Authenticate(
	ctx context.Context,
	credential IdentityCredential,
) (AuthenticatedIdentity, error) {
	if ctx == nil {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"%w: context is required",
			ErrInvalidRequest,
		)
	}
	if authenticator == nil ||
		nilInterface(authenticator.provider) ||
		nilInterface(authenticator.identities) ||
		nilInterface(authenticator.subjects) ||
		!validProviderName(authenticator.providerName) {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"%w: identity authenticator is not initialized",
			ErrInvalidState,
		)
	}
	if nilInterface(credential) ||
		!validProviderName(credential.CredentialKind()) {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"%w: typed identity credential is required",
			ErrInvalidRequest,
		)
	}

	external, err := authenticator.provider.Authenticate(ctx, credential)
	if err != nil {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"authenticate %s identity: %w",
			authenticator.providerName,
			err,
		)
	}
	if err := validateExternalIdentity(
		authenticator.providerName,
		external,
	); err != nil {
		return AuthenticatedIdentity{}, err
	}

	mapping, err := authenticator.identities.LookupIdentity(
		ctx,
		external.Provider,
		external.Subject,
	)
	if err != nil {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"lookup local identity: %w",
			err,
		)
	}
	if !validIdentityMapping(mapping) ||
		mapping.Provider != external.Provider ||
		mapping.Subject != external.Subject {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"%w: lookup returned a different identity",
			ErrInvalidIdentity,
		)
	}
	if !mapping.Active {
		return AuthenticatedIdentity{}, ErrIdentityInactive
	}

	subject, err := authenticator.subjects.LookupSubject(
		ctx,
		mapping.UserID,
	)
	if err != nil {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"lookup mapped subject: %w",
			err,
		)
	}
	if subject.ID != mapping.UserID || !validID(subject.ID) {
		return AuthenticatedIdentity{}, fmt.Errorf(
			"%w: lookup returned a different user",
			ErrInvalidSubject,
		)
	}
	if !subject.Active {
		return AuthenticatedIdentity{}, ErrSubjectInactive
	}

	external.Attributes = cloneIdentityAttributes(external.Attributes)
	return AuthenticatedIdentity{
		External: external,
		Mapping:  mapping,
		Subject:  subject,
	}, nil
}

func validateExternalIdentity(
	providerName string,
	identity ExternalIdentity,
) error {
	if identity.Provider != providerName ||
		!validProviderName(identity.Provider) ||
		!validIdentitySubject(identity.Subject) ||
		len(identity.Attributes) > maxIdentityAttributes {
		return ErrInvalidIdentity
	}
	for key, value := range identity.Attributes {
		if !validText(key, maxIdentityAttributeKey) ||
			sensitiveIdentityAttribute(key) ||
			(value != "" &&
				!validText(value, maxIdentityAttributeValue)) {
			return ErrInvalidIdentity
		}
	}
	return nil
}

func sensitiveIdentityAttribute(key string) bool {
	normalized := strings.Map(func(value rune) rune {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return unicode.ToLower(value)
		}
		return -1
	}, key)
	if normalized == "code" ||
		normalized == "otp" ||
		normalized == "pin" ||
		normalized == "authorization" {
		return true
	}
	for _, fragment := range []string{
		"password",
		"passwd",
		"passphrase",
		"token",
		"cookie",
		"secret",
		"credential",
		"verificationcode",
		"smscode",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func validIdentityMapping(mapping IdentityMapping) bool {
	return validProviderName(mapping.Provider) &&
		validIdentitySubject(mapping.Subject) &&
		validID(mapping.UserID) &&
		validText(mapping.Status, maxIdentityStatusBytes)
}

func validProviderName(value string) bool {
	if len(value) == 0 || len(value) > maxProviderBytes {
		return false
	}
	for index, character := range []byte(value) {
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index > 0 &&
			((character >= '0' && character <= '9') ||
				character == '.' ||
				character == '_' ||
				character == '-') {
			continue
		}
		return false
	}
	return true
}

func validIdentitySubject(value string) bool {
	return validText(value, maxIdentitySubjectBytes)
}

func cloneIdentityAttributes(
	attributes map[string]string,
) map[string]string {
	if attributes == nil {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}
