// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package acme

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ACME server response statuses used to describe Account, Authorization, and Challenge states.
const (
	StatusUnknown     = "unknown"
	StatusPending     = "pending"
	StatusProcessing  = "processing"
	StatusValid       = "valid"
	StatusInvalid     = "invalid"
	StatusRevoked     = "revoked"
	StatusDeactivated = "deactivated"
	StatusReady       = "ready"
)

// CRLReasonCode identifies the reason for a certificate revocation.
type CRLReasonCode int

// CRL reason codes as defined in RFC 5280.
const (
	CRLReasonUnspecified          CRLReasonCode = 0
	CRLReasonKeyCompromise        CRLReasonCode = 1
	CRLReasonCACompromise         CRLReasonCode = 2
	CRLReasonAffiliationChanged   CRLReasonCode = 3
	CRLReasonSuperseded           CRLReasonCode = 4
	CRLReasonCessationOfOperation CRLReasonCode = 5
	CRLReasonCertificateHold      CRLReasonCode = 6
	CRLReasonRemoveFromCRL        CRLReasonCode = 8
	CRLReasonPrivilegeWithdrawn   CRLReasonCode = 9
	CRLReasonAACompromise         CRLReasonCode = 10
)

// ErrUnsupportedKey is returned when an unsupported key type is encountered.
var ErrUnsupportedKey = errors.New("acme: unknown key type; only RSA and ECDSA are supported")

// Error is an ACME error as defined in RFC 7807, Problem Details for HTTP APIs.
type Error struct {
	// StatusCode is The HTTP status code generated by the origin server.
	StatusCode int

	// Type is a URI that identifies the problem type, typically in
	// a "urn:ietf:params:acme:error:xxx" form.
	Type string

	// Detail is a human-readable explanation specific to this occurrence of the problem.
	Detail string

	// Subproblems is an optional list of additional error information, usually
	// indicating problems with specific identifiers during authorization.
	Subproblems []Subproblem

	// Header is the original server error response headers.
	// It may be nil.
	Header http.Header
}

func (e *Error) Error() string {
	return fmt.Sprintf("acme: %s: %s", e.Type, e.Detail)
}

// An Subproblem is additional error detail that is included in an Error,
// usually indicating a problem with a specific identifier during authorization.
type Subproblem struct {
	// Type is a URI that identifies the subproblem type, typically in
	// "urn:ietf:params:acme:error:xxx" form.
	Type string

	// Detail is a human-readable explanation specific to this occurrence of the
	// subproblem.
	Detail string

	// Identifier optionally indicates the identifier that this subproblem is about.
	Identifier *AuthzID
}

// OrderInvalidError is returned when an order is marked as invalid.
type OrderInvalidError struct {
	// Order is the order that is invalid.
	Order *Order
}

func (e OrderInvalidError) Error() string {
	if e.Order == nil || e.Order.Error == nil {
		return "acme: order is invalid"
	}
	return fmt.Sprintf("acme: invalid order (%s): %s", e.Order.Error.Type, e.Order.Error.Detail)
}

// OrderPendingError is returned when an order is still pending after an
// attempted finalization.
type OrderPendingError struct {
	// Order is the order that is pending.
	Order *Order
}

func (e OrderPendingError) Error() string {
	return "acme: order is pending due to incomplete authorization"
}

// AuthorizationError is returned when an authorization is marked as invalid.
type AuthorizationError struct {
	// Authorization is the authorization that is invalid.
	Authorization *Authorization
}

func (e AuthorizationError) Error() string {
	if e.Authorization == nil {
		return "acme: authorization is invalid"
	}
	return fmt.Sprintf("acme: authorization for identifier %s is %s", e.Authorization.Identifier.Value, e.Authorization.Status)
}

// RateLimit reports whether err represents a rate limit error and
// any Retry-After duration returned by the server.
//
// See the following for more details on rate limiting:
// https://tools.ietf.org/html/draft-ietf-acme-acme-09#section-6.5
func RateLimit(err error) (time.Time, bool) {
	e, ok := err.(*Error)
	if !ok || e.Type != "urn:ietf:params:acme:error:rateLimited" {
		return time.Time{}, false
	}
	if e.Header == nil {
		return time.Time{}, true
	}
	return retryAfter(e.Header.Get("Retry-After")), true
}

// Account is a user account. It is associated with a private key.
type Account struct {
	// URL uniquely identifies the account.
	URL string

	// Status is the status of the account. Valid values are StatusValid,
	// StatusDeactivated, and StatusRevoked.
	Status string

	// Contact is a list of URLs that the server can use to contact the client
	// for issues related to this account.
	Contact []string

	Hmac []string
	KeyId []string

	// TermsAgreed indicates agreement with the terms of service. It is not
	// modifiable after account creation.
	TermsAgreed bool

	// OrdersURL is the URL used to fetch a list of orders submitted by this
	// account.
	OrdersURL string
}

// Directory is ACME server discovery data.
type Directory struct {
	// NewNonceURL is used to retrieve new nonces.
	NewNonceURL string

	// NewAccountURL is used to create new accounts.
	NewAccountURL string

	// NewOrderURL is used to create new orders.
	NewOrderURL string

	// NewAuthzURL is used to create new authorizations.
	NewAuthzURL string

	// RevokeCertURL is used to revoke a certificate.
	RevokeCertURL string

	// KeyChangeURL is used to change the account key.
	KeyChangeURL string

	// Terms is a URL identifying the current terms of service.
	Terms string

	// Website is an HTTP or HTTPS URL locating a website
	// providing more information about the ACME server.
	Website string

	// CAA consists of lowercase hostname elements, which the ACME server
	// recognises as referring to itself for the purposes of CAA record validation
	// as defined in RFC6844.
	CAA []string

	// ExternalAccountRequired, if true, indicates that the CA requires that all
	// new account requests include an ExternalAccountBinding field associating
	// the new account with an external account.
	ExternalAccountRequired bool
}

// NewOrder creates a new order with the domains provided, suitable for creating
// a TLS certificate order with CreateOrder.
func NewOrder(domains ...string) *Order {
	o := &Order{Identifiers: make([]AuthzID, len(domains))}
	for i, d := range domains {
		o.Identifiers[i] = AuthzID{
			Type:  "dns",
			Value: d,
		}
	}
	return o
}

// An Order represents a request for a certificate and is used to track the
// progress through to issuance.
type Order struct {
	// URL uniquely identifies the order.
	URL string

	// Status is the status of the order. It will be one of StatusPending,
	// StatusReady, StatusProcessing, StatusValid, and StatusInvalid.
	Status string

	// Expires is the teimstamp after which the server will consider the order invalid.
	Expires time.Time

	// Identifiers is a list of identifiers that the order pertains to.
	Identifiers []AuthzID

	// NotBefore is an optional requested value of the notBefore field in the certificate.
	NotBefore time.Time

	// NotAfter is an optional requested value of the notAfter field in the certificate.
	NotAfter time.Time

	// Error is the error that occurred while processing the order, if any.
	Error *Error

	// Authorizations is a list of URLs for authorizations that the client needs
	// to complete before the requested certificate can be issued. For
	// valid/invalid orders, these are the authorizations that were completed.
	Authorizations []string

	// FinalizeURL is the URL that is used to finalize the Order.
	FinalizeURL string

	// CertificateURL is the URL for the certificate that has been issued in
	// response to this order.
	CertificateURL string

	// RetryAfter is the timestamp, if any, to wait for before fetching this
	// order again.
	RetryAfter time.Time
}

// A Challenge is a CA challenge for an identifier.
type Challenge struct {
	// Type is the challenge type, e.g. "http-01" or "dns-01".
	Type string

	// URL is the URL where a challenge response can be posted.
	URL string

	// Token is a random value that uniquely identifies the challenge.
	Token string

	// Validated is the time at which the server validated this challenge.
	Validated time.Time

	// Status identifies the status of this challenge. Valid values are
	// StatusPending, StatusValid, and StatusInvalid.
	Status string

	// Error indicates the errors that occurred while the server was validating
	// this challenge.
	Error *Error
}

// Authorization encodes an authorization response.
type Authorization struct {
	// URL uniquely identifies the authorization.
	URL string

	// Status is the status of the authorization. Valid values are
	// StatusPending, StatusProcessing, StatusValid, StatusInvalid, and
	// StatusRevoked.
	Status string

	// Identifier is the identifier that the account is authorized to represent.
	Identifier AuthzID

	// Wildcard is true if the authorization is for the base domain of a wildcard identifier.
	Wildcard bool

	// Expires is the timestamp after which the server will consider this authorization invalid.
	Expires time.Time

	// Challenges is the list of challenges that the client can fulfill in order
	// to prove posession of the identifier. For valid/invalid authorizations,
	// this is the list of challenges that were used.
	Challenges []*Challenge
}

// AuthzID is an identifier that an account is authorized to represent.
type AuthzID struct {
	Type  string // The type of identifier, e.g. "dns".
	Value string // The identifier itself, e.g. "example.org".
}

type wireAuthzID struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// wireAuthz is ACME JSON representation of Authorization objects.
type wireAuthz struct {
	Status     string
	Challenges []wireChallenge
	Expires    time.Time
	Identifier struct {
		Type  string
		Value string
	}
	Wildcard bool
}

func (z *wireAuthz) authorization(url string) *Authorization {
	a := &Authorization{
		URL:        url,
		Status:     z.Status,
		Expires:    z.Expires,
		Identifier: AuthzID{Type: z.Identifier.Type, Value: z.Identifier.Value},
		Wildcard:   z.Wildcard,
		Challenges: make([]*Challenge, len(z.Challenges)),
	}
	for i, v := range z.Challenges {
		a.Challenges[i] = v.challenge()
	}
	return a
}

// wireChallenge is ACME JSON challenge representation.
type wireChallenge struct {
	URL       string
	Type      string
	Token     string
	Status    string
	Validated time.Time
	Error     *Error
}

func (c *wireChallenge) challenge() *Challenge {
	v := &Challenge{
		URL:       c.URL,
		Type:      c.Type,
		Token:     c.Token,
		Status:    c.Status,
		Validated: c.Validated,
		Error:     c.Error,
	}
	if v.Status == "" {
		v.Status = StatusUnknown
	}
	return v
}

// wireError is a subset of fields of the Problem Details object
// as described in https://tools.ietf.org/html/rfc7807#section-3.1.
type wireError struct {
	Status      int
	Type        string
	Detail      string
	Subproblems []Subproblem
}

func (e *wireError) error(h http.Header) *Error {
	return &Error{
		StatusCode:  e.Status,
		Type:        e.Type,
		Detail:      e.Detail,
		Subproblems: e.Subproblems,
		Header:      h,
	}
}

type wireOrder struct {
	Status         string
	Expires        time.Time
	Identifiers    []AuthzID
	NotBefore      time.Time
	NotAfter       time.Time
	Error          *Error
	Authorizations []string
	Finalize       string
	Certificate    string
}

func (o *wireOrder) order(url string, retryHeader string) *Order {
	return &Order{
		URL:            url,
		Status:         o.Status,
		Expires:        o.Expires,
		Identifiers:    o.Identifiers,
		NotBefore:      o.NotBefore,
		NotAfter:       o.NotAfter,
		Error:          o.Error,
		Authorizations: o.Authorizations,
		FinalizeURL:    o.Finalize,
		CertificateURL: o.Certificate,
		RetryAfter:     retryAfter(retryHeader),
	}
}
