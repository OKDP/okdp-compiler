package treeedit

import (
	"fmt"
	"sort"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// The keys of the entries this package edits: connections of
// compiler.PlatformFile and of a project's project.yaml, claims of a
// project's project.yaml.
const (
	connectionsKey = "connections"
	claimsKey      = "claims"
)

// ConnectionsFile is the file of the connections of a project,
// compiler.PlatformFile for the platform's ("").
func ConnectionsFile(project string) string {
	if project == "" {
		return compiler.PlatformFile
	}
	return ProjectFilePath(project)
}

// checkConnectionRef is checkRef of a connection, of a project or the
// platform's.
func checkConnectionRef(project, name string) error {
	if project == "" {
		if ValidateName("connection", name) != nil {
			return fmt.Errorf("connection %s: %w", name, ErrNotFound)
		}
		return nil
	}
	return checkRef("connection", project, name)
}

// connectionTarget names a connection in an edit's Target: platform/<name>
// for the platform's.
func connectionTarget(project, name string) string {
	if project == "" {
		return "platform/" + name
	}
	return project + "/" + name
}

// readEntries returns the entries of key (connections, claims) of a
// values file, and the file, nil without the file.
func readEntries(r Reader, file, key string) (map[string]any, []byte, error) {
	doc, raw, err := readValues(r, file)
	if err != nil {
		return nil, nil, err
	}
	entries, _ := doc[key].(map[string]any)
	return entries, raw, nil
}

// ListConnections returns the connections a project declares in its
// project.yaml, the platform's in platform.yaml for "", by name, as
// written (compiler.ParseConnection); none without the file.
func ListConnections(r Reader, project string) ([]compiler.Connection, error) {
	entries, _, err := readEntries(r, ConnectionsFile(project), connectionsKey)
	if err != nil {
		return nil, err
	}
	out := make([]compiler.Connection, 0, len(entries))
	for name, v := range entries {
		out = append(out, compiler.ParseConnection(name, v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ReadConnection returns one connection of a project, the platform's for
// "", or an error wrapping ErrNotFound.
func ReadConnection(r Reader, project, name string) (*compiler.Connection, error) {
	if err := checkConnectionRef(project, name); err != nil {
		return nil, err
	}
	entries, _, err := readEntries(r, ConnectionsFile(project), connectionsKey)
	if err != nil {
		return nil, err
	}
	v, ok := entries[name]
	if !ok {
		return nil, fmt.Errorf("connection %s: %w", name, ErrNotFound)
	}
	c := compiler.ParseConnection(name, v)
	return &c, nil
}

// PutConnection writes a connection of a project, or of the platform
// (project ""), an entry of connections of its file (ConnectionsFile)
// edited in place (EditEntry), checked: the instances that compiled
// still do. create fails with ErrExists when it is already declared; an
// update fails with ErrNotFound when it is not. A project must be
// declared. The description is kept on one line.
func PutConnection(project string, c compiler.Connection, create bool) (Edit, error) {
	if project != "" {
		if err := ValidateName("project", project); err != nil {
			return Edit{}, err
		}
	}
	if err := ValidateName("connection", c.Name); err != nil {
		return Edit{}, err
	}
	action := "update connection"
	if create {
		action = "create connection"
	}
	c.Description = strings.Join(strings.Fields(c.Description), " ")
	target := connectionTarget(project, c.Name)
	file := ConnectionsFile(project)
	return Edit{Action: action, Target: target, change: func(r Reader) (*Change, error) {
		entries, raw, err := readEntries(r, file, connectionsKey)
		if err != nil {
			return nil, err
		}
		if raw == nil && project != "" {
			return nil, fmt.Errorf("project %s: %w", project, ErrNotFound)
		}
		if err := checkEntry("connection", target, entries, c.Name, create); err != nil {
			return nil, err
		}
		return editEntry(raw, file, connectionsKey, c.Name, c, &Check{})
	}}, nil
}

// DeleteConnection removes a connection of a project, or of the platform
// (""). The check refuses it (*ErrInUse) while an instance references
// it. The change fails with ErrNotFound when it is not declared.
func DeleteConnection(project, name string) (Edit, error) {
	if err := checkConnectionRef(project, name); err != nil {
		return Edit{}, err
	}
	target := connectionTarget(project, name)
	file := ConnectionsFile(project)
	return Edit{Action: "delete connection", Target: target, change: func(r Reader) (*Change, error) {
		entries, raw, err := readEntries(r, file, connectionsKey)
		if err != nil {
			return nil, err
		}
		if _, ok := entries[name]; !ok {
			return nil, fmt.Errorf("connection %s: %w", target, ErrNotFound)
		}
		return editEntry(raw, file, connectionsKey, name, nil, &Check{Project: project, Removed: "connection " + name})
	}}, nil
}

// ListClaims returns the named claims of a project, entries of claims of
// its project.yaml, by name, as written (compiler.ParseClaim): a claim
// the compile refuses is listed all the same, with what it holds, the
// compile of the tree says why. An error wrapping ErrNotFound for a name
// that is not a project's.
func ListClaims(r Reader, project string) ([]compiler.ClaimSpec, error) {
	if ValidateName("project", project) != nil {
		return nil, fmt.Errorf("project %s: %w", project, ErrNotFound)
	}
	entries, _, err := readEntries(r, ProjectFilePath(project), claimsKey)
	if err != nil {
		return nil, err
	}
	var out []compiler.ClaimSpec
	for name, v := range entries {
		c, _ := compiler.ParseClaim(name, v)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// PutClaim writes a named claim of a project, an entry of claims of its
// project.yaml edited in place (EditEntry), checked: the instances that
// compiled still do. create fails with ErrExists when it is already
// declared; an update fails with ErrNotFound when it is not.
func PutClaim(project string, c compiler.ClaimSpec, create bool) (Edit, error) {
	if err := ValidateName("project", project); err != nil {
		return Edit{}, fmt.Errorf("project %s: %w", project, ErrNotFound)
	}
	if err := ValidateName("claim", c.Name); err != nil {
		return Edit{}, err
	}
	action := "update claim"
	if create {
		action = "create claim"
	}
	target := project + "/" + c.Name
	file := ProjectFilePath(project)
	return Edit{Action: action, Target: target, change: func(r Reader) (*Change, error) {
		entries, raw, err := readEntries(r, file, claimsKey)
		if err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, fmt.Errorf("project %s: %w", project, ErrNotFound)
		}
		if err := checkEntry("claim", target, entries, c.Name, create); err != nil {
			return nil, err
		}
		return editEntry(raw, file, claimsKey, c.Name, c, &Check{})
	}}, nil
}

// DeleteClaim removes a named claim of a project. The check refuses it
// (*ErrInUse) while an instance names it. Its provider keeps what the
// claim's connection holds. The change fails with ErrNotFound when it is
// not declared.
func DeleteClaim(project, name string) (Edit, error) {
	if err := checkRef("claim", project, name); err != nil {
		return Edit{}, err
	}
	target := project + "/" + name
	file := ProjectFilePath(project)
	return Edit{Action: "delete claim", Target: target, change: func(r Reader) (*Change, error) {
		entries, raw, err := readEntries(r, file, claimsKey)
		if err != nil {
			return nil, err
		}
		if _, ok := entries[name]; !ok {
			return nil, fmt.Errorf("claim %s: %w", target, ErrNotFound)
		}
		return editEntry(raw, file, claimsKey, name, nil, &Check{Project: project, Removed: "claim " + name})
	}}, nil
}

// checkEntry refuses to create an entry already there (ErrExists), or to
// update one that is not (ErrNotFound).
func checkEntry(kind, target string, entries map[string]any, name string, create bool) error {
	_, exists := entries[name]
	if create && exists {
		return fmt.Errorf("%s %s: %w", kind, target, ErrExists)
	}
	if !create && !exists {
		return fmt.Errorf("%s %s: %w", kind, target, ErrNotFound)
	}
	return nil
}

// editEntry is the change that writes file, raw, with its entry name of
// key set to value, or removed for nil (EditEntry), checked by check.
func editEntry(raw []byte, file, key, name string, value any, check *Check) (*Change, error) {
	data, err := EditEntry(raw, file, key, name, value)
	if err != nil {
		return nil, err
	}
	c := &Change{Check: check}
	c.write(file, data)
	return c, nil
}
