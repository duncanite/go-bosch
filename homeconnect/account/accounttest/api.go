// Package accounttest is the Home Connect account's API for tests: the
// token exchange a login ends with, the list of an account's appliances,
// and what opens each, on a loopback port, with a browser's part of a
// login played by Browse.
package accounttest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"

	"github.com/duncanite/go-bosch/homeconnect/account"
)

// What the fake account is: made up.
const (
	// Account is the account's id, which its tokens carry.
	Account = "account-0001"
	// Code is the code a login ends with.
	Code = "the code of one login"
)

// API is the fake, serving until Close.
type API struct {
	server *httptest.Server

	mu         sync.Mutex
	challenge  string
	spent      bool
	appliances []account.Appliance
	status     map[string]int
	raw        map[string]string
	requests   []string
}

// New starts the API with no appliance.
func New() *API {
	api := &API{status: map[string]int{}, raw: map[string]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /security/oauth/token", api.token)
	mux.HandleFunc("GET /api/account/v2/accounts/{account}/paired-appliances", api.paired)
	mux.HandleFunc("GET /api/appliance/v2/appliances/{id}/encryption-information", api.encryption)

	api.server = httptest.NewServer(mux)

	return api
}

// URL is the API's address, for both of a client's.
func (a *API) URL() string { return a.server.URL }

// Close stops the API.
func (a *API) Close() { a.server.Close() }

// Add gives the account an appliance, with what opens it.
func (a *API) Add(appliance account.Appliance) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.appliances = append(a.appliances, appliance)
}

// Status has a path answered with a status from now on; zero as before.
func (a *API) Status(path string, status int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.status[path] = status
}

// Raw has a path answered with a body as it is, under status 200.
func (a *API) Raw(path, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.raw[path] = body
}

// Requests is every request received, each as its method and its path.
func (a *API) Requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]string(nil), a.requests...)
}

// Browse plays the browser's part of a login: it reads the address a
// login is opened at, as the account's own page would, and gives the
// address the browser ends on, with the code. It panics on an address
// that is no login of this API's making.
func (a *API) Browse(login string) string {
	parsed, err := url.Parse(login)
	if err != nil {
		panic(err)
	}

	query := parsed.Query()

	if parsed.Path != "/security/oauth/authorize" || query.Get("response_type") != "code" ||
		query.Get("code_challenge_method") != "S256" || query.Get("redirect_uri") != "hcauth://auth/prod" ||
		query.Get("client_id") == "" || query.Get("scope") == "" || query.Get("state") == "" {
		panic("accounttest: not a login: " + login)
	}

	a.mu.Lock()
	a.challenge, a.spent = query.Get("code_challenge"), false
	a.mu.Unlock()

	return "hcauth://auth/prod?" + url.Values{
		"code": {Code}, "state": {query.Get("state")}, "grant_type": {"authorization_code"},
	}.Encode()
}

// overridden answers a path as Status or Raw said, and says whether it
// did.
func (a *API) overridden(writer http.ResponseWriter, request *http.Request) bool {
	a.mu.Lock()
	a.requests = append(a.requests, request.Method+" "+request.URL.Path)
	status, body := a.status[request.URL.Path], a.raw[request.URL.Path]
	a.mu.Unlock()

	switch {
	case status != 0:
		writer.WriteHeader(status)
	case body != "":
		_, _ = writer.Write([]byte(body))
	default:
		return false
	}

	return true
}

// token is the exchange a login ends with: the code, good once, and the
// verifier the login's challenge was made of.
func (a *API) token(writer http.ResponseWriter, request *http.Request) {
	if a.overridden(writer, request) {
		return
	}

	verifier := sha256.Sum256([]byte(request.PostFormValue("code_verifier")))

	a.mu.Lock()
	taken := !a.spent && a.challenge != "" &&
		base64.RawURLEncoding.EncodeToString(verifier[:]) == a.challenge &&
		request.PostFormValue("code") == Code &&
		request.PostFormValue("grant_type") == "authorization_code" &&
		request.PostFormValue("redirect_uri") == "hcauth://auth/prod" &&
		request.PostFormValue("client_id") != ""

	if taken {
		a.spent = true
	}

	a.mu.Unlock()

	if !taken {
		writer.WriteHeader(http.StatusBadRequest)

		return
	}

	claims, _ := json.Marshal(map[string]string{"sub": Account})
	token := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"

	_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": token})
}

// signedIn says a request carries the token this API gives.
func signedIn(request *http.Request) bool {
	token, bearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")

	return bearer && strings.HasPrefix(token, "header.") && strings.HasSuffix(token, ".signature")
}

func (a *API) paired(writer http.ResponseWriter, request *http.Request) {
	if a.overridden(writer, request) {
		return
	}

	if !signedIn(request) || request.PathValue("account") != Account {
		writer.WriteHeader(http.StatusUnauthorized)

		return
	}

	a.mu.Lock()
	listed := make([]map[string]string, 0, len(a.appliances))

	for _, one := range a.appliances {
		listed = append(
			listed,
			map[string]string{"haId": one.ID, "brand": one.Brand, "haType": one.Type, "vib": one.Model},
		)
	}

	a.mu.Unlock()

	_ = json.NewEncoder(writer).Encode(map[string]any{"appliances": listed})
}

func (a *API) encryption(writer http.ResponseWriter, request *http.Request) {
	if a.overridden(writer, request) {
		return
	}

	if !signedIn(request) {
		writer.WriteHeader(http.StatusUnauthorized)

		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, one := range a.appliances {
		if one.ID != request.PathValue("id") {
			continue
		}

		secret := map[string]string{"key": one.Key}
		mode := "tls"

		if one.Mode == account.AES {
			mode, secret["iv"] = "aes", one.Vector
		}

		_ = json.NewEncoder(writer).Encode(map[string]any{mode: secret})

		return
	}

	writer.WriteHeader(http.StatusNotFound)
}
