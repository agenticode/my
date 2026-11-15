# lenovo-shaped

A hand-built Redfish tree of one system and its BIOS settings. Every name is made up; no file comes from a real machine.

The attribute names (`Memory_MemorySpeed`, `Memory_MemoryTest`, `Memory_AdvMemTestOptions`, `Processors_CStates` and the
others), the registry name `BiosAttributeRegistry.1.0.0` and the pending settings at `/redfish/v1/Systems/1/Bios/Pending`
follow the examples of the "Lenovo XClarity Controller 2 REST API reference"
(<https://pubs.lenovo.com/xcc2-restapi/pending_bios_settings_get>,
<https://pubs.lenovo.com/xcc2-restapi/configure_amt_test_options>).

The tree holds no attribute registry, no memory and no log: it stands for a dump of the BIOS settings alone.

The tree is made by a script that is not part of the repository; change the files by hand.
