# DMTF message registries

`Platform.1.5.0.json`, `LogService.1.2.1.json` and `ResourceEvent.1.5.0.json` from the DMTF repository
Redfish-Publications (<https://github.com/DMTF/Redfish-Publications>, commit `fa6d23d`, 2026-10-13, directory
`registries/`), copied without changes. They are distributed under the BSD 3-Clause License; the licence and its
copyright notice are in [LICENSE.md](LICENSE.md).

The tests read them as a BMC would serve them, to check that the classifier takes the number of arguments and the
severity from the registry of the dump, and what the registries say about how much of their memory messages the
table `dmtf` covers.
