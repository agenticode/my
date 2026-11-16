package readers

import "github.com/agenticode/my/internal/model"

// discard is a sink that keeps nothing.
type discard struct{}

func (discard) Host(*model.Host) error               { return nil }
func (discard) Boot(*model.Boot) error               { return nil }
func (discard) Module(*model.Module) error           { return nil }
func (discard) Counter(*model.CounterSample) error   { return nil }
func (discard) Event(*model.Event) error             { return nil }
func (discard) Action(*model.Action) error           { return nil }
func (discard) GPU(*model.GPUMem) error              { return nil }
func (discard) ECS(*model.ECSSample) error           { return nil }
func (discard) BMC(*model.BMC) error                 { return nil }
func (discard) BIOSSetting(*model.BIOSSetting) error { return nil }
func (discard) BMCMemory(*model.BMCMemory) error     { return nil }
func (discard) Firmware(*model.FirmwareRecord) error { return nil }
