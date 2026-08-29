package contractmodel

func Declared(id, name, source, valueType string, allowed []string, change string, lifecycle ...Lifecycle) Setting {
	return Setting{ID: id, Name: name, Control: ControlDeclared, Description: name + " is supported operator choice.", Source: source, Type: valueType, AllowedValues: allowed, Sensitive: false, Lifecycle: lifecycle, Status: StatusImplemented, OperatorChange: change}
}

func Secret(id, name, source string, lifecycle ...Lifecycle) Setting {
	return Setting{ID: id, Name: name, Control: ControlSecret, Description: name + " is consumed without exposing its resolved value.", Source: source, Type: "string", Sensitive: true, Lifecycle: lifecycle, Status: StatusImplemented, OperatorChange: "Change the value in the selected Environment's SOPS document; media-stack never displays it."}
}

func Derived(id, name, source, description string, lifecycle ...Lifecycle) Setting {
	return Setting{ID: id, Name: name, Control: ControlDerived, Description: description, Source: source, Sensitive: false, Lifecycle: lifecycle, Status: StatusImplemented, OperatorChange: "Change the supporting Declared Configuration; this value is computed by media-stack."}
}

func Fixed(id, name string, value any, description string, lifecycle ...Lifecycle) Setting {
	return Setting{ID: id, Name: name, Control: ControlFixed, Description: description, Source: "Stack Policy#" + id, Type: valueType(value), Default: value, Sensitive: false, Lifecycle: lifecycle, Status: StatusImplemented, OperatorChange: "Cannot be changed through Declared Configuration because this is Stack Policy."}
}

func SensitiveDerived(id, name, source, description, change string, lifecycle ...Lifecycle) Setting {
	return Setting{ID: id, Name: name, Control: ControlDerived, Description: description, Source: source, Type: "string", Sensitive: true, Lifecycle: lifecycle, Status: StatusImplemented, OperatorChange: change}
}

func valueType(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case int, int64, float64:
		return "number"
	default:
		return "string"
	}
}

func External(id, name, source, owner string) Setting {
	return Setting{ID: id, Name: name, Control: ControlExternallySynchronized, Description: name + " is synchronized by " + owner + " and verified by media-stack.", Source: source, Owner: owner, Sensitive: false, Lifecycle: []Lifecycle{LifecycleSynchronize, LifecycleVerify}, Status: StatusImplemented, OperatorChange: "Not configurable through media-stack.yaml; propose a semantic Declared Configuration field before varying."}
}

func Unmanaged() Setting {
	return Setting{ID: "upstream.unlistedSettings", Name: "Unlisted upstream settings", Control: ControlUnmanaged, Description: "Upstream settings absent from this contract are Unmanaged Configuration.", Source: "upstream application", Sensitive: false, Lifecycle: []Lifecycle{LifecyclePreserve}, Status: StatusImplemented, OperatorChange: "Change through the upstream application at your own risk; media-stack does not promise to observe, apply, or repair it."}
}
