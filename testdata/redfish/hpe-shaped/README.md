# hpe-shaped

A hand-built Redfish tree. Every name, serial number and address is made up; no file comes from a real machine.

What follows a public document:

- The BIOS attribute names and their values (`AdvancedMemProtection`, `ExtendedMemTest`, `MaxMemBusFreqMHz`,
  `MemFastTraining`, `MemMirrorMode`, `MemPatrolScrubbing`, `MemRefreshRate`, `OpportunisticSelfRefresh`) follow HPE's
  "Bios resource definitions of iLO 5 v3.09"
  (<https://servermanagementportal.ext.hpe.com/docs/redfishservices/ilos/ilo5/ilo5_309/ilo5_bios_resourcedefns309>).
- The way from `Bios.AttributeRegistry` through `/redfish/v1/Registries/<name>` to a registry under
  `/redfish/v1/registrystore/registries/en/` follows HPE's "The standard Redfish BIOS data model"
  (<https://servermanagementportal.ext.hpe.com/docs/concepts/biosdatamodel>).
- The log entries use the text "Corrected Memory Error threshold exceeded (Processor 1, Memory Module 3)".

The `Memory` resources link no `Metrics`: this tree stands for a BMC that keeps no error counts per module. Everything else
is written from the DMTF schemas, with values of our own. It is not a copy of what an iLO answers.

The tree is made by a script that is not part of the repository; change the files by hand.
