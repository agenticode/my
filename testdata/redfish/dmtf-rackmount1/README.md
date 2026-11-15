# dmtf-rackmount1

A part of the mockup `public-rackmount1` of the DMTF Redfish Mockup Server
(<https://github.com/DMTF/Redfish-Mockup-Server>, branch `main`, fetched 2026-11-16), copied without changes.
The mockup is distributed under the BSD 3-Clause License; the licence and its copyright notice are in
[LICENSE.md](LICENSE.md). Every name, address and serial number in it is made up by its authors.

The files are laid out as `redfish/v1/<path>/index.json`. The mockup itself starts one level lower, at the
service root.

Copied: the service root, the system `437XR1138R2` with its `Memory`, `Bios`, `Bios/Settings` and
`LogServices`, the manager `BMC` with its `LogServices`, `Registries`, `UpdateService` with
`FirmwareInventory`, `Chassis`, and `SessionService`.

Also copied, to check that the collector never asks for them: `AccountService` with one account.

What this mockup shows a collector:

- `Bios` names the attribute registry `BiosAttributeRegistryP89.v1_0_0`, and `Registries` lists only the
  `Base` message registry: the registry of the BIOS attributes cannot be found on this service.
- The `Memory` resources link no `Metrics`.
- The log `Systems/437XR1138R2/LogServices/Log1/Entries` ends with a link to a next page
  (`?$skiptoken=2`) that the mockup has no file for. A server of static files answers it with the first
  page again.
