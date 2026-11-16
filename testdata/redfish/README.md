# Redfish fixtures

`standard-log.json` is written by hand: one log entry for each memory message of the DMTF registries `Platform` 1.5.0
and `LogService` 1.2.1 (https://github.com/DMTF/Redfish-Publications, `registries/`) and for the memory messages of
the OpenBMC registry (https://github.com/openbmc/bmcweb, `redfish-core/include/registries/openbmc.json`), one of
them under a renamed prefix, a `SELEntryAdded` entry that carries a SEL record of a memory sensor, and a SEL entry
whose event bytes stand in `MessageId` without `0x`. The message IDs and the order of the arguments follow the
registries. The texts and the modules are made up. It is no capture of a machine.
