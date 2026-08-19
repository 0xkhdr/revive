package providers

// InstallOrder is the fixed sequence step 10 walks. It is an explicit slice, never a map
// iteration: system managers before language managers, and reproducible across runs.
var InstallOrder = []string{
	"brew", "apt", "flatpak", "snap", "pacman", "dnf", "nix", "cargo", "pip",
}

// Registry maps a manifest package group to its provider. Adding a provider means adding one
// line here and one to InstallOrder, rather than editing four parallel switch statements.
type Registry struct {
	providers map[string]Provider
	node      *NodeProvider
	docker    Provider
}

// NewRegistry builds every provider from one set of dependencies.
func NewRegistry(d Deps, repoDir, home string) *Registry {
	return &Registry{
		providers: map[string]Provider{
			"brew":    NewBrew(d),
			"apt":     NewApt(d),
			"flatpak": NewFlatpak(d),
			"snap":    NewSnap(d),
			"pacman":  NewPacman(d),
			"dnf":     NewDnf(d),
			"nix":     NewNix(d),
			"cargo":   NewCargo(d),
			"pip":     NewPip(d),
		},
		node:   NewNode(d, repoDir, home),
		docker: NewDocker(d),
	}
}

// Get returns the provider for a package group.
func (r *Registry) Get(group string) (Provider, bool) {
	p, ok := r.providers[group]
	return p, ok
}

// Ordered returns the providers in install order, paired with their group names.
func (r *Registry) Ordered() []struct {
	Group    string
	Provider Provider
} {
	out := make([]struct {
		Group    string
		Provider Provider
	}, 0, len(InstallOrder))
	for _, group := range InstallOrder {
		if p, ok := r.providers[group]; ok {
			out = append(out, struct {
				Group    string
				Provider Provider
			}{group, p})
		}
	}
	return out
}

// Docker returns the image provider, which runs after the package managers.
func (r *Registry) Docker() Provider { return r.docker }

// Node returns the version provider, which runs last.
func (r *Registry) Node() *NodeProvider { return r.node }
