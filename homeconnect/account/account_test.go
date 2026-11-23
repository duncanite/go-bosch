package account_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/duncanite/go-bosch/homeconnect/account"
	"github.com/duncanite/go-bosch/homeconnect/account/accounttest"
)

// The appliances of the test account: made up.
func dishwasher() account.Appliance {
	return account.Appliance{
		ID: "THERMADOR-DWHD000XXX-000000000001", Brand: "THERMADOR", Type: "Dishwasher", Model: "DWHD000XXX",
		Mode: account.AES, Key: "a-key-of-the-AES-mode", Vector: "its-vector",
	}
}

func oven() account.Appliance {
	return account.Appliance{
		ID: "BOSCH-HBX000XXX-000000000002", Brand: "BOSCH", Type: "Oven", Model: "HBX000XXX",
		Mode: account.TLS, Key: "a-key-of-the-TLS-mode",
	}
}

func started(t *testing.T) (*accounttest.API, *account.Client) {
	t.Helper()

	api := accounttest.New()
	t.Cleanup(api.Close)

	client, err := account.New(account.Options{Login: api.URL(), Assets: api.URL()})
	if err != nil {
		t.Fatal(err)
	}

	return api, client
}

func signedIn(t *testing.T, api *accounttest.API, client *account.Client) account.Session {
	t.Helper()

	login, err := client.Begin()
	if err != nil {
		t.Fatal(err)
	}

	session, err := client.Finish(t.Context(), login, api.Browse(login.URL))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	return session
}

// TestLogin: a login is an address of the region's login API, made under
// the app's client id for a code, with a challenge and a state of its
// own each time; it is finished by the address the browser ends on, or by
// the code in it, and the account then gives its appliances, each with
// what opens it: a key and a vector for one of the AES mode, a key for
// one of the TLS mode.
func TestLogin(t *testing.T) {
	t.Parallel()

	api, client := started(t)
	api.Add(dishwasher())
	api.Add(oven())

	login, err := client.Begin()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := url.Parse(login.URL)
	if err != nil {
		t.Fatal(err)
	}

	query := parsed.Query()
	if !strings.HasPrefix(login.URL, api.URL()+"/security/oauth/authorize?") || query.Get("prompt") != "login" ||
		query.Get(
			"scope",
		) != "ReadOrigApi" || len(query.Get("client_id")) != 64 || len(query.Get("code_challenge")) != 43 ||
		query.Get("nonce") == "" {
		t.Errorf("a login is opened at %s", login.URL)
	}

	again, err := client.Begin()
	if err != nil || again.URL == login.URL {
		t.Errorf("a second login is opened at the same address, %v", err)
	}

	session, err := client.Finish(t.Context(), login, "  "+api.Browse(login.URL)+"\n")
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	appliances, err := client.Appliances(t.Context(), session)
	if want := []account.Appliance{dishwasher(), oven()}; err != nil || !reflect.DeepEqual(appliances, want) {
		t.Errorf("Appliances = %+v, %v\nwant %+v", appliances, err, want)
	}

	if want := []string{
		"POST /security/oauth/token",
		"GET /api/account/v2/accounts/account-0001/paired-appliances",
		"GET /api/appliance/v2/appliances/THERMADOR-DWHD000XXX-000000000001/encryption-information",
		"GET /api/appliance/v2/appliances/BOSCH-HBX000XXX-000000000002/encryption-information",
	}; !reflect.DeepEqual(api.Requests(), want) {
		t.Errorf("the account was asked %v", api.Requests())
	}

	// The code alone finishes a login too.
	login, err = client.Begin()
	if err != nil {
		t.Fatal(err)
	}

	api.Browse(login.URL)

	if _, err = client.Finish(t.Context(), login, accounttest.Code); err != nil {
		t.Errorf("Finish with the code alone: %v", err)
	}
}

// TestLoginsNotTaken: a code is good once; another login's address, a
// code that is none, an address with no code, and a login Begin did not
// make are refused, the first three as ErrLogin, before the account is
// asked when it can be told here.
func TestLoginsNotTaken(t *testing.T) {
	t.Parallel()

	api, client := started(t)

	login, err := client.Begin()
	if err != nil {
		t.Fatal(err)
	}

	ended := api.Browse(login.URL)

	if _, err = client.Finish(t.Context(), login, ended); err != nil {
		t.Fatal(err)
	}

	if _, err = client.Finish(t.Context(), login, ended); !errors.Is(err, account.ErrLogin) ||
		!strings.Contains(err.Error(), "status 400") {
		t.Errorf("a code used twice = %v, want ErrLogin", err)
	}

	other, err := client.Begin()
	if err != nil {
		t.Fatal(err)
	}

	before := len(api.Requests())

	for name, pasted := range map[string]string{
		"another login's address": ended,
		"an address with no code": "hcauth://auth/prod?code=&state=x",
		"nothing":                 "  ",
	} {
		if _, err = client.Finish(t.Context(), other, pasted); !errors.Is(err, account.ErrLogin) {
			t.Errorf("Finish with %s = %v, want ErrLogin", name, err)
		}
	}

	if len(api.Requests()) != before {
		t.Errorf("a login refused here was put to the account: %v", api.Requests()[before:])
	}

	api.Browse(other.URL)

	if _, err = client.Finish(t.Context(), other, "a code that is none"); !errors.Is(err, account.ErrLogin) {
		t.Errorf("a code that is none = %v, want ErrLogin", err)
	}

	if _, err = client.Finish(t.Context(), account.Login{}, accounttest.Code); !errors.Is(err, account.ErrArgument) {
		t.Errorf("Finish of a login Begin did not make = %v, want ErrArgument", err)
	}

	if _, err = client.Appliances(t.Context(), account.Session{}); !errors.Is(err, account.ErrArgument) {
		t.Errorf("Appliances with no session = %v, want ErrArgument", err)
	}
}

// TestAnswers: an account asked in the wrong region says so; a list that
// is refused, or cannot be read, is an error; an appliance whose key is
// not given is left out and named, and the others come; a token that
// names no account is not one. A context that ends is the context's.
func TestAnswers(t *testing.T) {
	t.Parallel()

	api, client := started(t)
	api.Add(dishwasher())
	api.Add(oven())

	session := signedIn(t, api, client)

	const (
		list = "/api/account/v2/accounts/account-0001/paired-appliances"
		key  = "/api/appliance/v2/appliances/BOSCH-HBX000XXX-000000000002/encryption-information"
	)

	api.Status(key, 404)

	appliances, err := client.Appliances(t.Context(), session)
	if want := []account.Appliance{dishwasher()}; !errors.Is(err, account.ErrUnread) ||
		!strings.Contains(err.Error(), "BOSCH-HBX000XXX-000000000002") || !reflect.DeepEqual(appliances, want) {
		t.Errorf("Appliances with one key refused = %+v, %v", appliances, err)
	}

	api.Status(key, 0)
	api.Raw(key, `{"aes":{"key":"only a key"}}`)

	if appliances, err = client.Appliances(
		t.Context(),
		session,
	); !errors.Is(err, account.ErrUnread) ||
		len(appliances) != 1 {
		t.Errorf("Appliances with an answer that holds no whole key = %+v, %v", appliances, err)
	}

	for name, row := range map[string]struct {
		status int
		raw    string
		want   error
	}{
		"the wrong region": {401, "", account.ErrRegion},
		"a refusal":        {503, "", account.ErrRefused},
		"no JSON":          {0, "<html>", account.ErrAnswer},
	} {
		api.Status(list, row.status)
		api.Raw(list, row.raw)

		if appliances, err = client.Appliances(t.Context(), session); !errors.Is(err, row.want) || appliances != nil {
			t.Errorf("Appliances with %s = %+v, %v; want %v", name, appliances, err, row.want)
		}
	}

	api.Status(list, 0)
	api.Raw(list, "")

	ended, cancel := context.WithCancel(t.Context())
	cancel()

	if appliances, err = client.Appliances(ended, session); !errors.Is(err, context.Canceled) || appliances != nil {
		t.Errorf("Appliances under a context that ended = %+v, %v", appliances, err)
	}

	for name, body := range map[string]string{
		"a token of one part":         `{"access_token":"opaque"}`,
		"a token that names no one":   `{"access_token":"a.e30.c"}`,
		"a token whose middle is not": `{"access_token":"a.!!.c"}`,
		"no JSON":                     `<html>`,
	} {
		login, beginErr := client.Begin()
		if beginErr != nil {
			t.Fatal(beginErr)
		}

		api.Raw("/security/oauth/token", body)

		if _, err = client.Finish(t.Context(), login, accounttest.Code); !errors.Is(err, account.ErrAnswer) {
			t.Errorf("Finish answered with %s = %v, want ErrAnswer", name, err)
		}
	}
}

// TestRegionsAndSecrets: a region names the account's two addresses, and
// one that is none is refused; a login shows its address and nothing of
// what finishes it, and a session nothing at all, however printed.
func TestRegionsAndSecrets(t *testing.T) {
	t.Parallel()

	for region, host := range map[account.Region]string{
		account.Europe: "https://api.home-connect.com/", account.NorthAmerica: "https://api-rna.home-connect.com/",
		account.China: "https://api.home-connect.cn/",
	} {
		client, err := account.New(account.Options{Region: region})
		if err != nil {
			t.Fatal(err)
		}

		if login, beginErr := client.Begin(); beginErr != nil || !strings.HasPrefix(login.URL, host) {
			t.Errorf("a login of %s is opened at %s, %v", region, login.URL, beginErr)
		}
	}

	if _, err := account.New(account.Options{Region: "mars"}); !errors.Is(err, account.ErrArgument) {
		t.Errorf("a region that is none = %v, want ErrArgument", err)
	}

	api, client := started(t)

	login, err := client.Begin()
	if err != nil {
		t.Fatal(err)
	}

	session, err := client.Finish(t.Context(), login, api.Browse(login.URL))
	if err != nil {
		t.Fatal(err)
	}

	var logged bytes.Buffer

	slog.New(slog.NewTextHandler(&logged, nil)).Info("signing in", "login", login, "session", session)

	shown := fmt.Sprintf("%v %+v %#v %v %+v %#v", login, login, login, session, session, session) + logged.String()
	if strings.Contains(shown, "signature") || strings.Contains(shown, accounttest.Account) ||
		strings.Contains(
			shown,
			"verifier",
		) || !strings.Contains(shown, "redacted") || !strings.Contains(shown, login.URL) {
		t.Errorf("a login and a session print as %s", shown)
	}
}

// TestProfile: the archive of an appliance's profile comes as the account
// keeps it, byte for byte, under the session; one the account has not is
// a refusal that names the appliance, the wrong region is said as such,
// and a call with no session or no appliance asks nothing.
func TestProfile(t *testing.T) {
	t.Parallel()

	api, client := started(t)
	api.Add(dishwasher())
	api.Describe(dishwasher().ID, []byte("PK\x03\x04 an archive, whatever it holds"))

	session := signedIn(t, api, client)

	archive, err := client.Profile(t.Context(), session, dishwasher().ID)
	if err != nil || string(archive) != "PK\x03\x04 an archive, whatever it holds" {
		t.Fatalf("Profile = %q, %v", archive, err)
	}

	if asked := api.Requests(); asked[len(asked)-1] != "GET /api/iddf/v1/iddf/"+dishwasher().ID {
		t.Errorf("the profile was asked for by %v", asked[len(asked)-1])
	}

	if _, err = client.Profile(t.Context(), session, oven().ID); !errors.Is(err, account.ErrRefused) ||
		!strings.Contains(err.Error(), oven().ID) || !strings.Contains(err.Error(), "404") {
		t.Errorf("Profile of an appliance the account has not described = %v, want ErrRefused", err)
	}

	api.Status("/api/iddf/v1/iddf/"+dishwasher().ID, 401)

	if _, err = client.Profile(t.Context(), session, dishwasher().ID); !errors.Is(err, account.ErrRegion) {
		t.Errorf("Profile answered 401 = %v, want ErrRegion", err)
	}

	before := len(api.Requests())

	for name, call := range map[string]func() error{
		"no session":   func() error { _, e := client.Profile(t.Context(), account.Session{}, dishwasher().ID); return e },
		"no appliance": func() error { _, e := client.Profile(t.Context(), session, ""); return e },
	} {
		if err = call(); !errors.Is(err, account.ErrArgument) {
			t.Errorf("Profile with %s = %v, want ErrArgument", name, err)
		}
	}

	if len(api.Requests()) != before {
		t.Errorf("a Profile that lacked what it needs asked the account: %v", api.Requests()[before:])
	}
}
