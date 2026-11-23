// Package account fetches, from the owner's Home Connect account, what
// opens each of its appliances on the local network: the key of the
// appliance's profile and, for one of the AES mode, its initial vector.
// It is asked once; nothing at run time needs the account again.
//
// BSH documents none of this. The account is reached as the Home Connect
// app reaches it, under the app's own client id, which is the same for
// everyone: the login is the owner's, in a browser, and this package
// never sees the password. What it gives is as secret as the appliances
// are private.
//
// The standard library alone.
package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// Region is the part of the world an account is kept in.
type Region string

// The regions BSH keeps accounts in.
const (
	Europe       Region = "eu"
	NorthAmerica Region = "na"
	China        Region = "cn"
)

const (
	// clientID is the Home Connect app's, which the login is made under.
	clientID = "9B75AC9EC512F36C84256AC47D813E2C1DD0D6520DF774B020E1E6E2EB29B1F3"
	// redirect is where a login ends: an address of the app's own, which
	// no browser opens.
	redirect = "hcauth://auth/prod"
	// scope is the least the account's API is read under.
	scope = "ReadOrigApi"
	// largest bounds an answer of the account's API.
	largest = 1 << 20
)

var (
	// ErrArgument is a call that was not given what it needs; nothing was
	// asked.
	ErrArgument = errors.New("account: bad argument")
	// ErrLogin is a login the account did not take: a code that is
	// wrong, used or too old, or an address that is another login's.
	ErrLogin = errors.New("account: the login was not taken")
	// ErrRegion is an account that is not kept where it was asked for.
	ErrRegion = errors.New("account: the account is not of this region")
	// ErrRefused is an answer of the account's API that is a refusal.
	ErrRefused = errors.New("account: refused")
	// ErrAnswer is an answer of the account's API this package cannot
	// read.
	ErrAnswer = errors.New("account: the answer cannot be read")
	// ErrUnread is an appliance of the account whose key was not given:
	// the others come all the same.
	ErrUnread = errors.New("account: an appliance's key was not given")
)

// Doer is what sends a request: an *http.Client, or anything like it.
type Doer interface {
	// Do sends a request and gives its response, as http.Client.Do does.
	Do(request *http.Request) (*http.Response, error)
}

// Options is where an account is, and what reaches it.
type Options struct {
	// Region is where the account is kept. It names the two addresses
	// below when they are empty.
	Region Region
	// Login and Assets are the addresses of the login's API and of the
	// account's, each a scheme and a host: empty are the region's.
	Login  string
	Assets string
	// HTTP sends the requests. Nil is a client that follows no redirect.
	HTTP Doer
}

// Client asks one region's account API.
type Client struct {
	login  string
	assets string
	http   Doer
}

// addresses is where each region's two APIs are.
//
//nolint:gochecknoglobals // a table, read only
var addresses = map[Region][2]string{
	Europe:       {"https://api.home-connect.com", "https://eu.services.home-connect.com"},
	NorthAmerica: {"https://api-rna.home-connect.com", "https://na.services.home-connect.com"},
	China:        {"https://api.home-connect.cn", "https://cn.services.home-connect.cn"},
}

// New is a client of the account API of a region, or of the two addresses
// given.
func New(options Options) (*Client, error) {
	client := &Client{login: options.Login, assets: options.Assets, http: options.HTTP}

	if client.login == "" || client.assets == "" {
		known, ok := addresses[options.Region]
		if !ok {
			return nil, fmt.Errorf("%w: the region %q is none of eu, na and cn", ErrArgument, options.Region)
		}

		if client.login == "" {
			client.login = known[0]
		}

		if client.assets == "" {
			client.assets = known[1]
		}
	}

	if client.http == nil {
		client.http = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}

	return client, nil
}

// redacted is what a secret of this package shows of itself.
const redacted = "redacted"

// Login is a login begun: the address to open in a browser, and what
// finishes it, which is secret and prints as none.
type Login struct {
	// URL is the address to open in a browser, where the owner signs in.
	URL string

	verifier string
	state    string
}

// String shows the address, and nothing of what finishes the login.
func (l Login) String() string { return "account.Login(" + l.URL + ")" }

// GoString is String.
func (l Login) GoString() string { return l.String() }

// LogValue is String.
func (l Login) LogValue() slog.Value { return slog.StringValue(l.String()) }

// Session is the account, signed in to. It prints as none.
type Session struct {
	token   string
	account string
}

// String shows that it is a session, and nothing of it.
func (Session) String() string { return "account.Session(" + redacted + ")" }

// GoString is String.
func (s Session) GoString() string { return s.String() }

// LogValue is String.
func (s Session) LogValue() slog.Value { return slog.StringValue(s.String()) }

// random is random bytes, as text of the URL alphabet.
func random(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("making a secret: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Begin makes a login: the address the owner opens in a browser to sign
// in to Home Connect. Signing in ends on an address that begins with
// hcauth://, which the browser cannot open: that address is what Finish
// takes.
func (c *Client) Begin() (Login, error) {
	const (
		verifierSize = 32
		stateSize    = 16
	)

	verifier, err := random(verifierSize)
	if err != nil {
		return Login{}, err
	}

	state, err := random(stateSize)
	if err != nil {
		return Login{}, err
	}

	nonce, err := random(stateSize)
	if err != nil {
		return Login{}, err
	}

	challenge := sha256.Sum256([]byte(verifier))

	query := url.Values{
		"response_type":         {"code"},
		"prompt":                {"login"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"client_id":             {clientID},
		"scope":                 {scope},
		"nonce":                 {nonce},
		"state":                 {state},
		"redirect_uri":          {redirect},
	}

	return Login{URL: c.login + "/security/oauth/authorize?" + query.Encode(), verifier: verifier, state: state}, nil
}

// Finish ends a login with where the browser was sent at its end: the
// whole address that begins with hcauth://, or the code in it alone. It
// is ErrLogin when the address is another login's, and when the account
// does not take the code: a code is good once, and not for long.
func (c *Client) Finish(ctx context.Context, login Login, ended string) (Session, error) {
	if login.verifier == "" {
		return Session{}, fmt.Errorf("%w: Finish needs the login Begin made", ErrArgument)
	}

	code := strings.TrimSpace(ended)

	if strings.Contains(code, "code=") {
		_, query, _ := strings.Cut(code, "?")

		values, err := url.ParseQuery(query)
		if err != nil {
			return Session{}, fmt.Errorf("%w: the address pasted cannot be read", ErrLogin)
		}

		if state := values.Get("state"); state != "" && state != login.state {
			return Session{}, fmt.Errorf("%w: the address pasted is another login's", ErrLogin)
		}

		code = values.Get("code")
	}

	if code == "" {
		return Session{}, fmt.Errorf("%w: no code was given", ErrLogin)
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code_verifier": {login.verifier},
		"code":          {code},
		"redirect_uri":  {redirect},
	}

	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.login+"/security/oauth/token", strings.NewReader(form.Encode()),
	)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrArgument, err)
	}

	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var granted struct {
		Token string `json:"access_token"` //nolint:tagliatelle // the API's key
	}

	status, err := c.exchange(request, &granted)

	switch {
	case err != nil:
		return Session{}, err
	case status != http.StatusOK:
		return Session{}, fmt.Errorf("%w: the account answered the code with status %d", ErrLogin, status)
	}

	account, err := subject(granted.Token)
	if err != nil {
		return Session{}, err
	}

	return Session{token: granted.Token, account: account}, nil
}

// subject is whose a token is: the account's id, which the token carries
// in its middle part.
func subject(token string) (string, error) {
	parts := strings.Split(token, ".")

	const signed = 3

	if len(parts) != signed {
		return "", fmt.Errorf("%w: the token is not one of three parts", ErrAnswer)
	}

	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", fmt.Errorf("%w: the token's middle part is not base64", ErrAnswer)
	}

	var claims struct {
		Subject string `json:"sub"`
	}

	if err = json.Unmarshal(payload, &claims); err != nil || claims.Subject == "" {
		return "", fmt.Errorf("%w: the token names no account", ErrAnswer)
	}

	return claims.Subject, nil
}

// The two modes an appliance speaks on the local network.
const (
	// AES is encryption in the application, under a key and an initial
	// vector.
	AES = "AES"
	// TLS is TLS under a pre-shared key.
	TLS = "TLS"
)

// Appliance is one appliance of the account, with what opens it.
type Appliance struct {
	// ID is the appliance's Home Connect id, which it announces on the
	// network.
	ID string
	// Brand, Type and Model are what it is, as the account says. Any may
	// be empty.
	Brand string
	Type  string
	Model string
	// Mode is which of the two modes it speaks: AES or TLS.
	Mode string
	// Key is the key of its profile, and Vector the initial vector of an
	// appliance of the AES mode, both as homeconnect.ParseKey takes
	// them. They are secrets.
	Key    string
	Vector string
}

// Appliances is the appliances of the account, each with its key. One
// whose key the account does not give is left out, and said: the error
// is then ErrUnread, naming it, and the others are returned with it. An
// account asked in the wrong region is ErrRegion.
func (c *Client) Appliances(ctx context.Context, session Session) ([]Appliance, error) {
	if session.token == "" {
		return nil, fmt.Errorf("%w: Appliances needs the session Finish gave", ErrArgument)
	}

	var paired struct {
		Appliances []listed `json:"appliances"`
	}

	status, err := c.get(
		ctx,
		session,
		"/api/account/v2/accounts/"+url.PathEscape(session.account)+"/paired-appliances",
		&paired,
	)

	switch {
	case err != nil:
		return nil, err
	case status == http.StatusUnauthorized:
		return nil, ErrRegion
	case status != http.StatusOK:
		return nil, fmt.Errorf("%w: the list of appliances, with status %d", ErrRefused, status)
	}

	appliances := make([]Appliance, 0, len(paired.Appliances))

	var unread []error

	for _, one := range paired.Appliances {
		if one.ID == "" {
			continue
		}

		appliance := Appliance{ID: one.ID, Brand: one.Brand, Type: one.Type, Model: one.Model}

		if err = c.unlock(ctx, session, &appliance); err != nil {
			if ctx.Err() != nil {
				return nil, err
			}

			unread = append(unread, err)

			continue
		}

		appliances = append(appliances, appliance)
	}

	return appliances, errors.Join(unread...)
}

// listed is an appliance as the account lists it.
type listed struct {
	ID    string `json:"haId"`
	Brand string `json:"brand"`
	Type  string `json:"haType"`
	Model string `json:"vib"`
}

// archived bounds the archive of an appliance's profile.
const archived = 16 << 20

// Profile is the archive of an appliance's profile, as the account keeps
// it: a zip of the two documents that name the appliance's values, which
// package profile reads. It is the account's own bytes, a stranger's
// until read, and holds no secret: the key is not in it.
func (c *Client) Profile(ctx context.Context, session Session, appliance string) ([]byte, error) {
	if session.token == "" || appliance == "" {
		return nil, fmt.Errorf("%w: Profile needs the session Finish gave, and an appliance's id", ErrArgument)
	}

	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, c.assets+"/api/iddf/v1/iddf/"+url.PathEscape(appliance), http.NoBody,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArgument, err)
	}

	request.Header.Set("Authorization", "Bearer "+session.token)

	status, archive, err := c.fetch(request, archived)

	switch {
	case err != nil:
		return nil, err
	case status == http.StatusUnauthorized:
		return nil, ErrRegion
	case status != http.StatusOK:
		return nil, fmt.Errorf("%w: the profile of %s, with status %d", ErrRefused, appliance, status)
	}

	return archive, nil
}

// get asks the account's API for a path, under the session, and reads a
// JSON answer of status 200 into into; it gives the status.
func (c *Client) get(ctx context.Context, session Session, path string, into any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.assets+path, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrArgument, err)
	}

	request.Header.Set("Authorization", "Bearer "+session.token)

	return c.exchange(request, into)
}

// exchange sends a request and reads a JSON answer of status 200 into
// into; it gives the status. An answer of another status is not read.
func (c *Client) exchange(request *http.Request, into any) (int, error) {
	request.Header.Set("Accept", "application/json")

	status, body, err := c.fetch(request, largest)
	if err != nil || status != http.StatusOK {
		return status, err
	}

	if err = json.Unmarshal(body, into); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrAnswer, err)
	}

	return status, nil
}

// fetch sends a request and gives its status, and the body of an answer
// of status 200, read within a bound. An answer of another status is not
// read.
func (c *Client) fetch(request *http.Request, bound int) (int, []byte, error) {
	response, err := c.http.Do(request)
	if err != nil {
		// A url.Error writes the whole address, the account's id and an
		// appliance's in it: the host says enough.
		if failed, wrapped := errors.AsType[*url.Error](err); wrapped {
			err = failed.Err
		}

		return 0, nil, fmt.Errorf("reaching the account at %s: %w", request.URL.Host, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, int64(bound)+1))
	if err != nil {
		return 0, nil, fmt.Errorf("reading the account's answer: %w", err)
	}

	if len(body) > bound {
		return 0, nil, fmt.Errorf("%w: longer than %d bytes", ErrAnswer, bound)
	}

	return response.StatusCode, body, nil
}

// unlock asks for what opens one appliance.
func (c *Client) unlock(ctx context.Context, session Session, appliance *Appliance) error {
	type secret struct {
		Key    string `json:"key"`
		Vector string `json:"iv"`
	}

	var given struct {
		AES *secret `json:"aes"`
		TLS *secret `json:"tls"`
	}

	status, err := c.get(ctx, session,
		"/api/appliance/v2/appliances/"+url.PathEscape(appliance.ID)+"/encryption-information", &given)

	switch {
	case err != nil:
		return fmt.Errorf("%w: %s: %w", ErrUnread, appliance.ID, err)
	case status != http.StatusOK:
		return fmt.Errorf("%w: %s: the account answered with status %d", ErrUnread, appliance.ID, status)
	case given.AES != nil && given.AES.Key != "" && given.AES.Vector != "":
		appliance.Mode, appliance.Key, appliance.Vector = AES, given.AES.Key, given.AES.Vector
	case given.TLS != nil && given.TLS.Key != "":
		appliance.Mode, appliance.Key = TLS, given.TLS.Key
	default:
		return fmt.Errorf("%w: %s: the account's answer holds no key", ErrUnread, appliance.ID)
	}

	return nil
}
