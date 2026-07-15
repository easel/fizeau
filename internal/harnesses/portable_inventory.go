package harnesses

import (
	"fmt"
	"sort"
)

// BuildPortableRuntimeInventory performs the stable, side-effect-free join of
// registry metadata to the actual runner-instance map owned by a configured
// service. It intentionally does not call Info, HealthCheck, Execute,
// PortableRuntimeAssets, LookPath, or any provider/routing surface.
func BuildPortableRuntimeInventory(registry *Registry, instances map[string]Harness) ([]PortableRuntimeSurface, error) {
	if registry == nil {
		return nil, fmt.Errorf("portable runtime inventory: nil registry")
	}

	names := make([]string, 0, len(registry.harnesses))
	for name := range registry.harnesses {
		names = append(names, name)
	}
	sort.Strings(names)

	seenInstances := make(map[string]struct{}, len(instances))
	rows := make([]PortableRuntimeSurface, 0, len(names))
	for _, name := range names {
		cfg := registry.harnesses[name]
		if name == "" || cfg.Name != name {
			return nil, fmt.Errorf("portable runtime inventory: invalid registry identity %q", name)
		}

		row := PortableRuntimeSurface{Name: name}
		switch {
		case cfg.TestOnly:
			row.Transport = PortableRuntimeTransportEmbedded
			row.Inclusion = PortableRuntimeInclusionTestOnly
		case cfg.IsHTTPProvider:
			if cfg.Binary != "" {
				return nil, fmt.Errorf("portable runtime inventory: HTTP row %q declares a subprocess binary", name)
			}
			row.Transport = PortableRuntimeTransportHTTP
			row.Inclusion = PortableRuntimeInclusionNonSubprocess
		case name == "fiz":
			row.Transport = PortableRuntimeTransportEmbedded
			row.Inclusion = PortableRuntimeInclusionNonSubprocess
		default:
			instance, ok := instances[name]
			if !ok || instance == nil {
				return nil, fmt.Errorf("portable runtime inventory: subprocess row %q has no actual runner instance", name)
			}
			seenInstances[name] = struct{}{}
			descriptor, ok := instance.(PortableRuntimeStructuralHarness)
			if !ok {
				return nil, fmt.Errorf("portable runtime inventory: runner %q has no structural descriptor", name)
			}
			structure := descriptor.PortableRuntimeStructure()
			if structure.Name != name {
				return nil, fmt.Errorf("portable runtime inventory: runner %q reports structural identity %q", name, structure.Name)
			}
			row.Instance = instance
			row.Transport = structure.Transport
			switch row.Transport {
			case PortableRuntimeTransportNative, PortableRuntimeTransportEmbedded, PortableRuntimeTransportHTTP:
				if structure.Mode != PortableRuntimeStructuralNonSubprocess {
					return nil, fmt.Errorf("portable runtime inventory: non-subprocess runner %q reports mode %q", name, structure.Mode)
				}
				row.Inclusion = PortableRuntimeInclusionNonSubprocess
			case PortableRuntimeTransportSubprocess:
				if cfg.Binary == "" {
					return nil, fmt.Errorf("portable runtime inventory: subprocess row %q has no binary identity", name)
				}
				switch {
				case structure.Mode == PortableRuntimeStructuralUnpinned:
					row.Inclusion = PortableRuntimeInclusionRequired
				case structure.Mode == PortableRuntimeStructuralExactPinOnly && cfg.ExactPinSupport:
					row.Inclusion = PortableRuntimeInclusionExactPinOnly
				default:
					return nil, fmt.Errorf("portable runtime inventory: subprocess row %q has invalid structural mode %q", name, structure.Mode)
				}
			default:
				return nil, fmt.Errorf("portable runtime inventory: runner %q reports unknown transport %q", name, row.Transport)
			}
		}
		rows = append(rows, row)
	}

	for name, instance := range instances {
		if instance == nil {
			return nil, fmt.Errorf("portable runtime inventory: nil runner instance %q", name)
		}
		if _, ok := seenInstances[name]; ok {
			continue
		}
		return nil, fmt.Errorf("portable runtime inventory: runner instance %q has no subprocess registry row", name)
	}
	return rows, nil
}
