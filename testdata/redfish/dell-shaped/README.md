# dell-shaped

A hand-built Redfish tree. Every name, serial number and address is made up; no file comes from a real machine.

What follows a public document:

- The BIOS attribute names and their values (`AdddcSetting`, `CECriticalSEL`, `CorrEccSmi`, `MemFrequency`, `MemOpMode`,
  `MemPatrolScrub`, `MemRefreshRate`, `MemTest`, `PersistentMemPassphrase`) follow the "Integrated Dell Remote Access
  Controller 9 Attribute Registry", 2020, Rev. A00
  (<https://dl.dell.com/topicspdf/idrac9-lifecycle-controller-v4x-series_Reference-Guide_en-us.pdf>), where they are listed as
  `BIOS.MemSettings.<name>` and `BIOS.SysProfileSettings.<name>`.
- That the Redfish `Attributes` use the last part of those names, and the URI of the registry
  (`/redfish/v1/Systems/System.Embedded.1/Bios/BiosRegistry`), follow Dell's script `GetSetBiosAttributesREDFISH.py`
  (<https://github.com/dell/iDRAC-Redfish-Scripting>).
- The log entries use message IDs of the form `MEMnnnn` with the text "Correctable memory error rate exceeded for DIMM_A1."

Everything else (the `Memory`, `MemoryMetrics`, `Manager` and `LogService` resources and the attribute registry itself) is
written from the DMTF schemas, with values of our own. It is not a copy of what an iDRAC answers.

The tree is made by a script that is not part of the repository; change the files by hand.
