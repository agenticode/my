package bmctest

import (
	"fmt"
	"strings"
)

// Machine is a made-up service with what a server with memory errors has:
// DIMMs with metrics, BIOS settings with their registry, several logs,
// a firmware inventory. It is returned as an overlay for a stand-in: dimms
// modules, a system event log of selEntries entries, and a service root
// that says whether a client may ask for $expand.
func Machine(dimms, selEntries int, expand bool) map[string]string {
	t := map[string]string{}
	coll := func(path string, members ...string) {
		var refs []string
		for _, m := range members {
			refs = append(refs, fmt.Sprintf(`{"@odata.id":%q}`, m))
		}
		t[path] = fmt.Sprintf(`{"@odata.id":%q,"Name":"collection","Members@odata.count":%d,"Members":[%s]}`, path, len(members), strings.Join(refs, ","))
	}
	features := ""
	if expand {
		features = `"ProtocolFeaturesSupported":{"ExpandQuery":{"ExpandAll":true,"Levels":true,"MaxLevels":2,"Links":true,"NoLinks":true}},`
	}
	t["/redfish/v1"] = `{"@odata.id":"/redfish/v1/","@odata.type":"#ServiceRoot.v1_15_0.ServiceRoot","Id":"RootService","RedfishVersion":"1.17.0","Vendor":"Example","Product":"Example BMC",` + features +
		`"Systems":{"@odata.id":"/redfish/v1/Systems"},"Managers":{"@odata.id":"/redfish/v1/Managers"},"Chassis":{"@odata.id":"/redfish/v1/Chassis"},` +
		`"Registries":{"@odata.id":"/redfish/v1/Registries"},"UpdateService":{"@odata.id":"/redfish/v1/UpdateService"},` +
		`"AccountService":{"@odata.id":"/redfish/v1/AccountService"},"SessionService":{"@odata.id":"/redfish/v1/SessionService"},` +
		`"Links":{"Sessions":{"@odata.id":"/redfish/v1/SessionService/Sessions"}}}`
	t["/redfish/v1/AccountService"] = `{"@odata.id":"/redfish/v1/AccountService","Id":"AccountService"}`
	coll("/redfish/v1/Systems", "/redfish/v1/Systems/1")
	t["/redfish/v1/Systems/1"] = `{"@odata.id":"/redfish/v1/Systems/1","@odata.type":"#ComputerSystem.v1_20_0.ComputerSystem","Id":"1","Manufacturer":"Example","Model":"EX-2000","HostName":"node-a","BiosVersion":"1.4.2",` +
		`"Memory":{"@odata.id":"/redfish/v1/Systems/1/Memory"},"Bios":{"@odata.id":"/redfish/v1/Systems/1/Bios"},"LogServices":{"@odata.id":"/redfish/v1/Systems/1/LogServices"}}`
	var mem []string
	for i := 1; i <= dimms; i++ {
		p := fmt.Sprintf("/redfish/v1/Systems/1/Memory/DIMM%d", i)
		mem = append(mem, p)
		t[p] = fmt.Sprintf(`{"@odata.id":%q,"@odata.type":"#Memory.v1_17_0.Memory","Id":"DIMM%d","Name":"DIMM %d","DeviceLocator":"DIMM_A%d","CapacityMiB":32768,"SerialNumber":"TEST%04d","Status":{"State":"Enabled","Health":"OK"},"Metrics":{"@odata.id":"%s/MemoryMetrics"}}`, p, i, i, i, i, p)
		t[p+"/MemoryMetrics"] = fmt.Sprintf(`{"@odata.id":"%s/MemoryMetrics","@odata.type":"#MemoryMetrics.v1_7_0.MemoryMetrics","Id":"MemoryMetrics","CurrentPeriod":{"CorrectableECCErrorCount":%d,"UncorrectableECCErrorCount":0},"LifeTime":{"CorrectableECCErrorCount":%d,"UncorrectableECCErrorCount":0}}`, p, i, 10*i)
	}
	coll("/redfish/v1/Systems/1/Memory", mem...)
	t["/redfish/v1/Systems/1/Bios"] = `{"@odata.id":"/redfish/v1/Systems/1/Bios","@odata.type":"#Bios.v1_2_0.Bios","Id":"Bios","AttributeRegistry":"BiosAttributeRegistryEX.v1_0_0","Attributes":{"PatrolScrub":"Enabled","AdminPassword":null},` +
		`"@Redfish.Settings":{"SettingsObject":{"@odata.id":"/redfish/v1/Systems/1/Bios/Settings"}},"Actions":{"#Bios.ResetBios":{"target":"/redfish/v1/Systems/1/Bios/Actions/Bios.ResetBios"}}}`
	t["/redfish/v1/Systems/1/Bios/Settings"] = `{"@odata.id":"/redfish/v1/Systems/1/Bios/Settings","@odata.type":"#Bios.v1_2_0.Bios","Id":"Settings","Attributes":{"PatrolScrub":"Disabled"}}`
	coll("/redfish/v1/Registries", "/redfish/v1/Registries/Base.1.8.0", "/redfish/v1/Registries/BiosAttributeRegistryEX.v1_0_0")
	t["/redfish/v1/Registries/Base.1.8.0"] = `{"@odata.id":"/redfish/v1/Registries/Base.1.8.0","Id":"Base.1.8.0","Registry":"Base.1.8.0","Location":[{"Language":"en","Uri":"/redfish/v1/Registries/Base.1.8.0/Base.1.8.0.json"}]}`
	t["/redfish/v1/Registries/BiosAttributeRegistryEX.v1_0_0"] = `{"@odata.id":"/redfish/v1/Registries/BiosAttributeRegistryEX.v1_0_0","@odata.type":"#MessageRegistryFile.v1_1_0.MessageRegistryFile","Id":"BiosAttributeRegistryEX.v1_0_0","Registry":"BiosAttributeRegistryEX.v1_0_0",` +
		`"Location":[{"Language":"ja","Uri":"/redfish/v1/RegistryStore/ja/BiosAttributeRegistryEX.json"},{"Language":"en","Uri":"/redfish/v1/RegistryStore/en/BiosAttributeRegistryEX.json","PublicationUri":"https://vendor.example/registries/BiosAttributeRegistryEX.v1_0_0.json"}]}`
	t["/redfish/v1/RegistryStore/en/BiosAttributeRegistryEX.json"] = `{"@odata.type":"#AttributeRegistry.v1_3_0.AttributeRegistry","Id":"BiosAttributeRegistryEX.v1_0_0","Language":"en","RegistryVersion":"1.0.0","RegistryEntries":{"Attributes":[{"AttributeName":"PatrolScrub","Type":"Enumeration","Value":[{"ValueName":"Enabled"},{"ValueName":"Disabled"}]}]}}`
	t["/redfish/v1/RegistryStore/ja/BiosAttributeRegistryEX.json"] = `{"Id":"BiosAttributeRegistryEX.v1_0_0","Language":"ja"}`

	logs := func(base string, ids ...string) {
		var members []string
		for _, id := range ids {
			members = append(members, base+"/"+id)
			t[base+"/"+id] = fmt.Sprintf(`{"@odata.id":"%s/%s","@odata.type":"#LogService.v1_4_0.LogService","Id":%q,"Name":"log","Entries":{"@odata.id":"%s/%s/Entries"},"Actions":{"#LogService.ClearLog":{"target":"%s/%s/Actions/LogService.ClearLog"}}}`, base, id, id, base, id, base, id)
		}
		coll(base, members...)
	}
	entries := func(path string, n int) {
		var list []string
		for i := 1; i <= n; i++ {
			list = append(list, fmt.Sprintf(`{"@odata.id":"%s/%d","@odata.type":"#LogEntry.v1_15_0.LogEntry","Id":"%d","EntryType":"SEL","Created":"2026-11-%02dT01:00:00Z","SensorType":"Memory","Message":"Correctable ECC DIMM_A1","Severity":"Warning"}`, path, i, i, 1+i%28))
		}
		t[path] = fmt.Sprintf(`{"@odata.id":%q,"Name":"entries","Members@odata.count":%d,"Members":[%s]}`, path, n, strings.Join(list, ","))
	}
	logs("/redfish/v1/Systems/1/LogServices", "SEL", "Journal", "AuditLog", "HostLogger")
	entries("/redfish/v1/Systems/1/LogServices/SEL/Entries", selEntries)
	entries("/redfish/v1/Systems/1/LogServices/Journal/Entries", 3)
	entries("/redfish/v1/Systems/1/LogServices/AuditLog/Entries", 3)
	entries("/redfish/v1/Systems/1/LogServices/HostLogger/Entries", 3)

	coll("/redfish/v1/Managers", "/redfish/v1/Managers/1")
	t["/redfish/v1/Managers/1"] = `{"@odata.id":"/redfish/v1/Managers/1","@odata.type":"#Manager.v1_15_0.Manager","Id":"1","Model":"Example BMC","FirmwareVersion":"2.14.0","DateTime":"2026-11-16T03:02:00+00:00","LogServices":{"@odata.id":"/redfish/v1/Managers/1/LogServices"}}`
	logs("/redfish/v1/Managers/1/LogServices", "EventLog")
	entries("/redfish/v1/Managers/1/LogServices/EventLog/Entries", 2)

	t["/redfish/v1/UpdateService"] = `{"@odata.id":"/redfish/v1/UpdateService","Id":"UpdateService","FirmwareInventory":{"@odata.id":"/redfish/v1/UpdateService/FirmwareInventory"}}`
	coll("/redfish/v1/UpdateService/FirmwareInventory", "/redfish/v1/UpdateService/FirmwareInventory/BIOS", "/redfish/v1/UpdateService/FirmwareInventory/BMC")
	t["/redfish/v1/UpdateService/FirmwareInventory/BIOS"] = `{"@odata.id":"/redfish/v1/UpdateService/FirmwareInventory/BIOS","@odata.type":"#SoftwareInventory.v1_8_0.SoftwareInventory","Id":"BIOS","Name":"System BIOS","Version":"1.4.2"}`
	t["/redfish/v1/UpdateService/FirmwareInventory/BMC"] = `{"@odata.id":"/redfish/v1/UpdateService/FirmwareInventory/BMC","@odata.type":"#SoftwareInventory.v1_8_0.SoftwareInventory","Id":"BMC","Name":"BMC firmware","Version":"2.14.0"}`
	coll("/redfish/v1/Chassis", "/redfish/v1/Chassis/1")
	t["/redfish/v1/Chassis/1"] = `{"@odata.id":"/redfish/v1/Chassis/1","@odata.type":"#Chassis.v1_23_0.Chassis","Id":"1","ChassisType":"RackMount","Manufacturer":"Example","Model":"EX-2000"}`
	return t
}
