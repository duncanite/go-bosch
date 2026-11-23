# go-bosch

A Go library and command for BSH Home Connect appliances (Bosch, Thermador, Siemens,
Gaggenau) on the LAN, with no cloud at run time.

An appliance announces itself on the local network and serves a WebSocket there, which a
client with the key of the appliance's profile can speak. This module finds appliances
and speaks to one of the AES mode: what it says of itself, the values it holds, what it
tells as it changes. Both were run against one Thermador dishwasher. The key comes once
from the owner's Home Connect account; fetching it is not built yet.

The command, run from the appliances' network:

```bash
just homeconnect discover          # the appliances that announce themselves, and where
just homeconnect discover --whole  # all each one announces

export HOMECONNECT_KEY=... HOMECONNECT_IV=...   # the key and initial vector of the appliance's profile
just homeconnect info              # its brand, its model, its versions
just homeconnect values            # what it holds, each value under its numeric id
just homeconnect watch             # what it says as it changes, until interrupted
just homeconnect call get /ro/allMandatoryValues   # anything else, as the appliance answers it
```

With one appliance on the network the command finds it; among several, `--id` says which,
and `--host` names an address and skips the search.

[docs/design.md](docs/design.md) is the research the module starts from, its shape and
its decisions.
