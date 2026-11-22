# go-bosch

A Go library and command for BSH Home Connect appliances (Bosch, Thermador, Siemens,
Gaggenau) on the LAN, with no cloud at run time.

So far it finds them: an appliance announces itself on the local network, and the command
prints the ones it hears, which was run against one Thermador dishwasher. Reading and
driving an appliance over its own WebSocket is what comes next, and has not been tried.

The command, run from the appliances' network:

```bash
just homeconnect discover          # the appliances that announce themselves, and where
just homeconnect discover --whole  # all each one announces
```

[docs/design.md](docs/design.md) is the research the module starts from, its shape and
its decisions.
