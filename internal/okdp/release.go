package okdp

// PlatformObjectNames: the names of the objects the platform writes in the
// namespace of every project besides its instances' (the targets'): the
// Role and the NetworkPolicy okdp-project, the control plane's Role and
// RoleBinding, the RoleBindings of the project's groups, and the secret
// store's defaults: the ServiceAccount and the DCR token's Secret. The
// names the platform's settings give the store, its ServiceAccount and the
// token are added to them (platformObjects). The targets' own constants
// are checked against them (output.TestPlatformObjectNames).
func PlatformObjectNames() []string { return defaultRules().list(tablePlatformObjectNames) }

// checkRelease: nil when release is none of the platform's objects, nor
// the prefix of one up to a "-" (the rule set's release check); okdp is the
// platform's settings.
func checkRelease(rules *RuleSet, field, release string, okdp M) error {
	return rules.refuse(checkReleaseRule, M{"field": field, "release": release, "settings": okdp})
}
