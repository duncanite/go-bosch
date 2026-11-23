# go-bosch

A Go library and command for BSH Home Connect appliances (Bosch, Thermador, Siemens,
Gaggenau) on the LAN, with no cloud at run time.

An appliance announces itself on the local network and serves a WebSocket there, which a
client with the key of the appliance's profile can speak. This module finds appliances
and speaks to one of the AES mode: what it says of itself, the values it holds, what it
tells as it changes. The key comes once from the owner's Home Connect account, which
`account` fetches after a sign-in in the owner's browser. All of it was run against one
Thermador dishwasher and its owner's account.

The command, run from the appliances' network:

```bash
just homeconnect discover          # the appliances that announce themselves, and where
just homeconnect discover --whole  # all each one announces

just homeconnect account --region na --profiles ~/.config/homeconnect   # eu, na or cn: sign in; prints each appliance's key and initial vector, writes its profile
export HOMECONNECT_KEY=... HOMECONNECT_IV=...   # what account printed for the appliance
export HOMECONNECT_PROFILE=~/.config/homeconnect/<id>.zip   # what account wrote for it: names its values
just homeconnect info              # its brand, its model, its versions
just homeconnect features          # what the profile says it has
just homeconnect values            # what it holds, each value by its name
just homeconnect watch             # what it says as it changes, until interrupted
just homeconnect call get /ro/allMandatoryValues   # anything else, as the appliance answers it
```

With one appliance on the network the command finds it; among several, `--id` says which,
and `--host` names an address and skips the search.

[docs/design.md](docs/design.md) is the research the module starts from, its shape and
its decisions.
