# go-bosch: the design

Research into BSH Home Connect appliances (Bosch, Thermador, Siemens, Gaggenau), a
Thermador dishwasher in view, done on 2026-11-15 from public sources, and the module it
led to. The module finds appliances on the network and speaks the AES mode of the
protocol, and both were run against one dishwasher.

In short: a Home Connect appliance serves a WebSocket on the LAN, and with the keys of
its own profile a client can read and drive it there with no cloud at run time. The keys
come from the owner's Home Connect account, once. BSH documents none of this; its
supported interface is a cloud API. The local protocol is written down by others, in Go
among them.

## The device

A Home Connect appliance joins the home's Wi-Fi and is paired to an account in the Home
Connect app. BSH's own description is that "direct access to home appliances is not
possible" and that its cloud API is the way in
([how it works](https://developer.home-connect.com/how_it_works)). What the appliance
sends to BSH's servers, and what the app does on the LAN, is not documented.

## Ways in

### The appliance's own WebSocket (local)

From the protocol notes of [go-homeconnect2mqtt](https://github.com/SukramJ/go-homeconnect2mqtt)
(`docs/01-protocol.md`) and from [hcpy](https://github.com/osresearch/hcpy), the first
public implementation:

- The appliance listens at `/homeconnect`, in one of two modes that its profile names
  (`connectionType`):
  - **AES**: `ws://<host>:80`, the messages encrypted in the application, AES-CBC with a
    chained HMAC-SHA256, both keys derived from the appliance's pre-shared key.
  - **TLS**: `wss://<host>:443`, TLS 1.2 with that pre-shared key. hcpy saw a dishwasher
    negotiate `ECDHE-PSK-CHACHA20-POLY1305`.
- Messages are JSON: a session id, a message id, a resource, an action, data. After a
  short handshake the appliance gives every value at once, then pushes changes.
- Read: the operation state, the door, the remaining time, errors, each under a numeric
  id that the profile's feature description names. Do: set a value, run a command, select
  and start a program; starting is where appliances differ most.
- An appliance announces itself over DNS-SD as `_homeconnect._tcp`, with a TXT record
  that carries its Home Connect id (`id`), its brand, its kind (`type`) and its model as
  BSH abbreviates it (`vib`): what the Home Assistant integration
  [reads to find one](https://github.com/chris-mc1/homeconnect_local_hass/blob/baa64d14a53c5e4777512117790b81d272361858/custom_components/homeconnect_ws/config_flow.py).
- The pre-shared key (and for AES an initial vector), with the feature descriptions, come
  from the account: [the profile downloader](https://github.com/bruestel/homeconnect-profile-downloader)
  or hcpy's own login fetch them once. That login is the account's, in a browser, with a
  CAPTCHA.
- A dishwasher reports `RemoteControlStartAllowed`: starting a cycle wants the remote
  start enabled on the appliance, which opening the door clears. Nothing found says the
  local protocol goes around it.

Go's standard library has no pre-shared-key cipher suites, so the TLS mode needs another
TLS implementation; go-homeconnect2mqtt does it with OpenSSL through cgo, behind a build
tag. The AES mode is plain Go.

### BSH's cloud API (official)

- `https://api.home-connect.com`, with a simulator at `simulator.home-connect.com`
  ([general documentation](https://api-docs.home-connect.com/general/)).
- OAuth: the authorization code flow and a device flow, scopes by appliance type
  ([authorization](https://api-docs.home-connect.com/authorization/)).
- Events as a stream per appliance.
- Limits, per client and account: 1,000 requests a day, 50 a minute, 5 program starts a
  minute, 10 token refreshes a minute and 100 a day, 10 event channels.
- Thermador is among the brands covered. Remote start has to be enabled on the appliance
  and is cleared when the door opens.

### Matter

Bosch's [United States FAQ](https://www.bosch-home.com/us/owner-support/get-support/home-connect/home-connect-faq)
says Matter is rolling out for some appliances, refrigerators first. Nothing found shows a
dishwasher or a Thermador with it.

## Two ways in, compared

| | The appliance's WebSocket | The cloud API |
|---|---|---|
| Supported by BSH | no | yes |
| Local at run time | yes | no |
| Needs | the profile's keys, fetched once from the account | a developer account, OAuth consent |
| Limits | none known | 1,000 requests a day |
| Risk | a firmware update can change the protocol; the key download depends on a login that already gained a CAPTCHA | a cloud in the path |

## What this module is

A client of the appliance's own WebSocket, local at run time, built in steps. The cloud
API is not taken: it puts BSH's cloud in the path of every reading, under a daily quota.

### Shape

- `homeconnect`: the library.
  - An appliance's announcement: `Recognize` says whether an instance of
    `_homeconnect._tcp` is an appliance's and which, and `Find` is the search, through
    a `Browser` the caller lends.
  - A session with an appliance of the AES mode: `ParseKey` reads the key and the
    initial vector of its profile, `Dial` opens the WebSocket and does the handshake,
    and a `Session` asks (`Get`, `Post`), tells (`Notify`) and hears (`Next`), with
    `Services`, `Identity`, `Close`, `Done` and `Err`. A refusal is a `RefusedError`
    with the appliance's code.
- `homeconnect/account`: what opens each appliance, from the owner's Home Connect
  account. `Begin` makes a login, an address the owner opens in a browser; `Finish`
  ends it with the address the browser was sent to; `Appliances` lists the account's
  appliances, each with its key and, for the AES mode, its initial vector; `Profile`
  gives the archive of an appliance's profile. `accounttest` is the account's API for
  tests. Standard library only.
- `homeconnect/profile`: what an appliance's profile says of its values. `FromArchive`
  reads the archive the account gives, `Parse` its two documents; a `Profile` gives the
  `Feature` an id stands for, with its name, its kind, its access and the names of what
  an enumerated one may be, and the id a name stands for. Standard library only.
- `homeconnect/homeconnecttest`: an appliance for tests. The WebSocket of the AES mode
  on a loopback port, under a key made for tests, which opens sessions, answers the
  handshake, holds values and tells of their changes, and on demand refuses, stalls,
  hangs up, sends a frame out of the chain or goes deaf.
- `internal/aeschain`: the AES mode's encryption, one chain a direction, used by the
  library and by the test appliance.
- `cmd/homeconnect` and `internal/cli`: the command, kong under primordium's process
  setup, with the Multicast DNS appliances are searched for over. `discover` prints the
  appliances that announce themselves, `account` fetches the keys and the profiles, and
  `features` prints a profile. Under the appliance's key, read from the environment: `info`, what it says of itself; `services`; `values`, what it holds,
  each under its numeric id; `watch`, what it says as it changes; and `call`, any
  request.
- `internal/terminal`: every line the command prints goes through it, so that what an
  appliance wrote is shown and never obeyed.

### Decisions

- **Finding an appliance needs a means this module does not have, and does not take.** A
  host has one Multicast DNS responder, and it belongs to the program, not to a library.
  `Recognize` is the knowledge, with no socket; `Find` is the search, through a `Browser`
  declared here in this module's own terms, which names no Multicast DNS library. The
  command has the program's, [go-mdns](https://github.com/forkcloser/go-mdns), and a
  lint rule keeps it to `internal/cli`.
- **An appliance is its Home Connect id.** It is what the announcement carries and what a
  profile names the appliance by. An instance of the service with no id is not taken for
  an appliance.
- **An announcement is a claim.** Anyone on the network can announce an id at an address
  of their choosing. What opens an appliance is the key of its profile, which is not on
  the network.
- **The announced port is kept as announced.** The two modes listen on two ports, 80 and
  443, and what an appliance announces may say which it speaks: not known until one is
  seen, so `Found` gives each address with the port that came with it, and nothing is
  read into it yet.
- **Discovery first.** What the dishwasher in view announces, its port above all, says
  which of the two modes the rest has to speak, and so what is built next.
- **The AES mode, and not the TLS one.** The dishwasher in view speaks AES, which the
  standard library covers. A key with no initial vector is an appliance's of the TLS
  mode, and `Dial` says it is not supported: nothing here needs pre-shared-key TLS yet.
- **The encryption is a chain, and a chain cannot lose a frame.** Every message of a
  direction is one stream of AES-CBC, and each frame's tag covers the tag before it:
  a frame opens only after the one before it, so one that never comes leaves every
  later one unreadable. A frame that does not authenticate moves nothing, and the one
  that belongs in its place would still open; but a client cannot tell a frame altered
  from one foreign or one come in the place of a missing one, so the first that fails
  ends the session. The reference implementation reads on, and after a missing frame
  floods with failures. The first frame of a session that does not authenticate is the
  key's doing, and is said as `ErrKey`; a later one is a session lost. For the same
  reason nothing is sealed, and no id taken, for a request whose context has already
  ended: a frame sealed and not sent is a frame the appliance never gets. A write that
  fails ends the session.
- **The key proves who speaks, not when.** A key and its vector never change, and every
  connection starts its chain from them: whoever recorded a session on the network can
  play the appliance's side of it again to a client, which would take old readings for
  new. It is the protocol's own, and nothing a client does closes it.
- **A request its caller gave up on is forgotten.** Its answer, should it come, goes to
  `Next` as any message that answers nothing waited for; nothing is kept for it.
- **Checked against OpenSSL.** The test of `internal/aeschain` opens two frames made
  with OpenSSL's AES-256-CBC and HMAC-SHA256 and none of this module's code: one stream
  across both, each tag over the one before, the second message of the length whose
  padding takes a block of its own.
- **What is said and not asked for is kept, to a bound.** `Next` gives what the
  appliance notifies of, in order. More than a thousand messages unread ends the
  session as `ErrOverrun`: a message is not dropped without a word.
- **A message that cannot be read ends nothing.** It authenticated, so the chain
  stands; `Next` gives it whole, as an `UnreadError`, and the one after it next.
- **A code that says all went well is no refusal.** The reference implementation takes
  any code in an answer for an error; here an answer with a code from 200 to 299 is an
  answer.
- **The client names itself the same way every time**: a fixed name and id, unless a
  `Dialer` gives others. An appliance keeps the clients it has seen, by id, and tells
  the list at each connection: a client that named itself anew each time would add to
  it without end. What a second connection under the same id does to the first is not
  known.
- **The WebSocket library is confined.** [gorilla/websocket](https://github.com/gorilla/websocket)
  carries the frames, under a license the baseline accepts; a lint rule keeps it to
  `homeconnect` and its test appliance, and none of its types cross the library's API.
- **The key is the environment's.** The command reads `HOMECONNECT_KEY` and
  `HOMECONNECT_IV` and takes a key no other way; a `Key` prints as redacted under fmt
  and slog, and refuses to be marshalled.
- **The key is fetched as the app fetches it.** BSH gives a key to its own app and to
  nothing else, so the account is asked under the app's client id, the same for
  everyone, as [hcpy's login](https://github.com/hcpy2-0/hcpy/blob/590f1e71a75c91b36ea77eab7b5698c42ace4451/hc-login.py)
  and the [profile downloader](https://github.com/bruestel/homeconnect-profile-downloader/blob/bd05ae35bc661182ea17b08a2f3a3b90a7f51f71/main.js)
  do: an authorization-code login with a proof key, the least scope that reads the
  account, then the list of appliances and each one's encryption information.
- **The owner signs in, in their own browser.** The login page carries a CAPTCHA, and a
  password is no command's to hold. The login ends on an address of the app's own,
  `hcauth://`, which no browser opens: the owner copies it from the browser's network
  tab and pastes it to the command. That step is clumsy and is all there is, short of
  embedding a browser. The address pasted is checked against the login it belongs to
  before the account is asked.
- **A region is said, not guessed.** An account is kept in Europe, North America or
  China, each with its own addresses; `account` asks for `--region` and tries no other.
- **One appliance's key refused does not hide the others.** They are returned, and
  printed, with an error that names the one left out.
- **What `account` prints is a secret**, the last two columns of each line: it goes to
  the terminal it was asked from, and from there to the environment.
- **A profile names what an appliance says; it opens nothing.** An appliance speaks in
  numeric ids, and the two documents of its profile, a description of its structure
  and a mapping of ids to names, say what each is. The account gives them in one
  archive, with no key in it. `account --profiles` writes each appliance's archive
  into a directory, under the appliance's id, and `--profile` names one: `values` and
  `watch` then print each value by its name, and as the member it is when it is
  enumerated; `features` prints what the profile holds, with no key and no appliance.
- **The archive is kept as the account gave it.** It is read to know it is a profile
  before it is written, and written byte for byte: nothing here re-encodes it, so
  whatever reads it later reads the account's own. A library takes it as bytes; the
  file is the command's.
- **A profile is a stranger's text.** The archive and each document in it are read
  within a bound, whatever length the archive claims; an appliance's id is a file's
  name only when it is letters, digits, dots, dashes and underscores; and a name the
  mapping gives is printed through `internal/terminal` like anything an appliance says.
- **A description is walked, a mapping is decoded.** Features are nested in lists to any
  depth, so the description is read element by element, and whatever carries an id and
  is a known kind is a feature; a list is none, nor is an option a program refers to.
  An enumeration the mapping does not name is a part of one it does. A value that is
  no member of its enumeration, as some firmware sends, is shown as it came.
- **`info` shows the make, not the appliance.** Its brand, model and versions; the
  serial and the hardware address an appliance also gives come only with `--whole`.
- **Among several appliances the command chooses none.** A key is one appliance's:
  with no `--host`, the one appliance on the network is spoken to, and among several
  `--id` says which.

### Next

1. What a consumer needs of an appliance as values with types: its door, its state, its
   remaining time, read from a session under a profile.
2. Setting a value, selecting and starting a program.
3. The TLS mode, when an appliance needs it: pre-shared-key TLS, which Go's standard
   library does not have.

## Observed on an appliance

On 2026-11-22 the command's `discover --whole` was run on a network with one Home
Connect appliance, a Thermador dishwasher, and heard it within its wait:

- It announces one instance of `_homeconnect._tcp`, at one IPv4 address and port 80.
- Its TXT record has eight entries: `id`, `brand`, `type` and `vib`, as the Home
  Assistant integration reads them; and `info`, the kind again in capitals; `mac`, twelve
  hexadecimal digits; `txtvers`, 2; and `vers`, `1.0-2.17.1.13549`.
- Its `id` is its brand, its `vib` and its `mac`, joined by dashes, and `vib` is the
  model its owner had given, `DWHD650WFM`. Its host's name is its `id`, then `.local`.

Port 80 is where the AES mode listens, and a host named by the id alone is what the
notes of go-homeconnect2mqtt give as an AES appliance's default host, where a TLS one's
has the brand and the kind before the id. So this dishwasher looks to speak AES, the
mode Go's standard library can speak. An announcement does not prove it: the profile's
`connectionType` says which, and so does the port that answers.

The same day, a TCP connection to that address was taken at port 80 and refused at port
443: the port of the AES mode answers, and the TLS mode's does not.

Then the command was run against it, under the key and the initial vector its account
gives for it:

- It opened a session, and the handshake went through as it is written here. Its
  services are `ci` 2, `ei` 2, `ni` 1 and `ro` 1: so it was sent a nonce, asked what it
  is, told the client was ready and asked of its network, and it has no `iz`.
- What it said of itself, to `/ci/info`: its brand, its `vib`, an `eNumber` that is the
  `vib` with a slash and an index, `deviceInfo` and `deviceType`, its kind in two
  spellings, `haVersion` 1.0, a `swVersion` that is the announcement's `vers` without
  its first part, and a `hwVersion`.
- `/ro/allMandatoryValues` answered with a hundred values, each a number, a truth value
  or a string, under ids from 3 to 5135.
- Under `watch` it first said, unasked, `/ci/registeredDevices`: the clients it knows,
  each with a number, a kind, a name, an id and whether it is connected. Two were its
  owner's, not connected; the third was this module, under its fixed name and id,
  connected. So an appliance keeps the clients it has seen, by the id each gave.
- Then, as something was done at the appliance, it told of single values changing in
  `/ro/values`; then `/ro/descriptionChange`, some twenty ids each with a parent and an
  access; then every value again, in several notifications of about thirty values each.
- Of three commands that searched for it, each for three seconds, one did not hear it;
  named by its address it answered at once.

The key had come, the same day, from the `account` command, run once against the
owner's account in North America:

- The owner signed in, in a browser, at the address the command printed. The browser
  was last sent to `/security/oauth/redirect_target` on the login's own host, with a
  code and a state that are that host's and not this login's; the answer to that
  request redirects to the `hcauth://` address, in its `Location` header, and the
  browser says only that it did not go there. The address to copy is that header's.
- Pasted to the command, it was taken: the account gave a token, and its list held one
  appliance, the dishwasher, of the AES mode, with a key and an initial vector.
- That key opened the dishwasher.

Later that day `account --profiles` was run against the same account, and `features`
and `values` under the profile it wrote:

- The account gave the dishwasher's profile as one archive, which read as a profile and
  was written under the appliance's id.
- The profile describes sixty-three features, every one with a name: thirty-six under
  `BSH.Common`, the rest under `Dishcare.Dishwasher`; commands, settings, statuses,
  events, options and six programs. Their access is written in the words this module
  compares in lower case, `read`, `readwrite`, `writeonly`, `readstatic` and `none`.
- The members of its enumerations are written by their last word, `Closed`, `Off`,
  `Inactive`, and not by the whole name BSH's public documentation gives them.
- Of the hundred values the dishwasher holds, forty-five are features of its profile
  and were printed by name, the enumerated ones as their member; fifty-five are under
  ids the profile does not describe at all, and were printed by their id.

## Open questions

- Whether the dishwasher's profile names the AES mode, as its announcement and its ports
  say: see [Observed on an appliance](#observed-on-an-appliance).
- Whether the command should start a cycle, or reading the state is enough: starting
  meets the remote-start button each time.

## Not verified

- Beyond [Observed on an appliance](#observed-on-an-appliance): `call`, a `Post`, a
  refusal by a real appliance, a session kept for longer than a minute and its pings,
  and what an appliance of version 1 or 3 of a service wants were run against this
  module's test appliance only, which was written from the same notes as the client.
- Of the account's API: an account in Europe or in China, an appliance of the TLS mode,
  an account with several appliances, and every refusal were run against this module's
  stand-in only, written from the two implementations named above.
- What an appliance answers a wrong key with: here, its first frame fails to
  authenticate. Whether a real one sends that frame at all is not known.
- A profile other than one dishwasher's: an enumeration that is a part of another, a
  profile whose members are written by their whole names, and a feature the mapping
  does not name were read in documents made for the tests only.
- What the fifty-five values the dishwasher holds under ids its profile does not
  describe are.
- What an appliance in the TLS mode announces, its port above all.
- No report of a Thermador dishwasher on the local protocol was found; reports for
  Thermador ovens, hoods and refrigerators were, through the Home Assistant integration
  [homeconnect_local_hass](https://github.com/chris-mc1/homeconnect_local_hass).
- Whether any Go TLS library supports the cipher suite the TLS mode needs.
- What the Matter dishwasher device type would carry; it was not re-read for this.

## Sources

- [go-homeconnect2mqtt](https://github.com/SukramJ/go-homeconnect2mqtt): the local
  protocol written out, and a Go implementation of it.
- [homeconnect_websocket](https://github.com/chris-mc1/homeconnect_websocket/tree/42482e39ac24e9125c452622225aab9675d2e049/src/homeconnect_websocket):
  the reference implementation, in Python, that the session, the handshake and the
  encryption here follow (`session.py`, `message.py`, `hc_socket.py`), and
  [go-homeconnect2mqtt's notes on it](https://github.com/SukramJ/go-homeconnect2mqtt/blob/38765e5f3bb56c4a6db25caf65fe2bbf992b8542/docs/01-protocol.md).
- [hcpy](https://github.com/osresearch/hcpy) and its maintained fork
  [hcpy2-0/hcpy](https://github.com/hcpy2-0/hcpy): the first implementation, the cipher
  suite seen on a dishwasher, the login with a CAPTCHA.
- [homeconnect-profile-downloader](https://github.com/bruestel/homeconnect-profile-downloader):
  fetching a profile.
- [homeconnect_local_hass](https://github.com/chris-mc1/homeconnect_local_hass): local
  use in Home Assistant, Thermador reports.
- Home Connect: [how it works](https://developer.home-connect.com/how_it_works),
  [general documentation](https://api-docs.home-connect.com/general/),
  [authorization](https://api-docs.home-connect.com/authorization/).
- [Bosch's Home Connect FAQ](https://www.bosch-home.com/us/owner-support/get-support/home-connect/home-connect-faq):
  Matter in North America.
