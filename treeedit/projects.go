package treeedit

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"

	compiler "github.com/okdp/okdp-compiler"
)

// Project is a project of the tree, projects/<p>/ with its project.yaml:
// a namespace. A project exists when that file does, whoever wrote it: a
// console and a person editing the files declare projects the same way.
// Its description is the key description of project.yaml
// (compiler.ProjectDescription), which the compiler leaves out of the
// context of the project's instances.
type Project struct {
	Name        string
	Description string
	// ReadErr is why its project.yaml could not be read, a project that
	// ProjectsFile lists (split, ListProjects): listed all the same,
	// without its description. Nil otherwise.
	ReadErr error
}

// readProject reads projects/<name>/project.yaml.
func readProject(r Reader, name string) (*Project, error) {
	raw, err := r.ReadFile(ProjectFilePath(name))
	if err != nil {
		return nil, err
	}
	return &Project{Name: name, Description: ReadDescription(raw)}, nil
}

// ReadProject returns one project, or an error wrapping ErrNotFound.
func ReadProject(r Reader, name string) (*Project, error) {
	if err := ValidateName("project", name); err != nil {
		return nil, fmt.Errorf("project %s: %w", name, ErrNotFound)
	}
	return readProject(r, name)
}

// ListProjects returns the projects of the tree, by name: the
// directories of projects/ holding a project.yaml. split: the projects
// each have a repository of their own, listed in ProjectsFile, the
// directories of projects/ r gives: each is a project, even while its
// project.yaml cannot be read (Project.ReadErr).
func ListProjects(r Reader, split bool) ([]Project, error) {
	out := []Project{}
	names, err := r.ReadDir(compiler.ProjectsDir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if ValidateName("project", name) != nil || (!split && !r.Exists(ProjectFilePath(name))) {
			continue
		}
		p, err := readProject(r, name)
		if err != nil {
			if !split {
				return nil, err
			}
			p = &Project{Name: name, ReadErr: err}
		}
		out = append(out, *p)
	}
	return out, nil
}

// CheckProjectName refuses a project name the compile refuses, every
// instance of the project with it (compiler.CheckProjectName): not a
// name, one okdp-compiler reserves (platform, kube-*, the deployer
// engines' and the targets' namespaces) or one of the platform's,
// global.okdp.policy.reserved of platform.yaml as r holds it. The
// refusal is a *CompileError, the compiler's message: the edit's fault.
// A program that creates more of a project than the tree (its Namespace,
// its repository) checks it first.
func CheckProjectName(r Reader, name string) error {
	platform, _, err := readValues(r, compiler.PlatformFile)
	if err != nil {
		return err
	}
	if err := compiler.CheckProjectName(name, platform); err != nil {
		return &CompileError{Project: name, Err: err}
	}
	return nil
}

// CheckNewProject refuses a project of a tree whose projects each have a
// repository of their own (split) that the tree has already, listed in
// ProjectsFile (ErrExists): for a program that creates the repository of
// a project before its CreateProject.
func CheckNewProject(r Reader, name string) error {
	if r.Exists(ProjectDir(name)) {
		return fmt.Errorf("project %s: %w", name, ErrExists)
	}
	return nil
}

// CreateProject writes the project.yaml of a new project, its
// description (WriteDescription). The change fails with ErrExists when
// the project is already declared. split: the projects each have a
// repository of their own, and the project is added to ProjectsFile too;
// its repository is the caller's (CheckNewProject). The caller checks
// the name first (CheckProjectName).
func CreateProject(p Project, split bool) (Edit, error) {
	if err := ValidateName("project", p.Name); err != nil {
		return Edit{}, err
	}
	file := ProjectFilePath(p.Name)
	data, err := WriteDescription(nil, file, p.Description)
	if err != nil {
		return Edit{}, err
	}
	return Edit{Action: "create project", Target: p.Name, change: func(r Reader) (*Change, error) {
		if r.Exists(file) && (!split || r.Exists(ProjectDir(p.Name))) {
			return nil, fmt.Errorf("project %s: %w", p.Name, ErrExists)
		}
		c := &Change{}
		c.write(file, data)
		if !split {
			return c, nil
		}
		if err := updateProjectsList(r, c, func(list *ProjectsList) {
			if list.Entry(p.Name) == nil {
				list.Projects = append(list.Projects, ProjectEntry{Name: p.Name})
			}
		}); err != nil {
			return nil, err
		}
		return c, nil
	}}, nil
}

// UpdateProject changes the description of a declared project. The rest
// of project.yaml is kept as written; an empty description removes its
// key. The change fails with ErrNotFound when the project is not
// declared.
func UpdateProject(p Project) (Edit, error) {
	if err := ValidateName("project", p.Name); err != nil {
		return Edit{}, fmt.Errorf("project %s: %w", p.Name, ErrNotFound)
	}
	file := ProjectFilePath(p.Name)
	return Edit{Action: "update project", Target: p.Name, change: func(r Reader) (*Change, error) {
		raw, err := r.ReadFile(file)
		if err != nil {
			return nil, err
		}
		data, err := WriteDescription(raw, file, p.Description)
		if err != nil {
			return nil, err
		}
		c := &Change{}
		if !bytes.Equal(data, raw) {
			c.write(file, data)
		}
		return c, nil
	}}, nil
}

// DeleteProject removes a project and everything it declares; the next
// compile removes its compiled tree. The check refuses it (*ErrInUse)
// while an instance of another project uses one of its instances. split:
// the projects each have a repository of their own, and the project
// leaves ProjectsFile, its repository left as it is. The change fails
// with ErrNotFound when the tree has no such project directory.
func DeleteProject(project string, split bool) (Edit, error) {
	if err := ValidateName("project", project); err != nil {
		return Edit{}, fmt.Errorf("project %s: %w", project, ErrNotFound)
	}
	return Edit{Action: "delete project", Target: project, change: func(r Reader) (*Change, error) {
		if !r.Exists(ProjectDir(project)) {
			return nil, fmt.Errorf("project %s: %w", project, ErrNotFound)
		}
		c := &Change{Check: &Check{Project: project, Removed: "project " + project}}
		if !split {
			c.remove(ProjectDir(project))
			return c, nil
		}
		if err := updateProjectsList(r, c, func(list *ProjectsList) {
			kept := list.Projects[:0]
			for _, p := range list.Projects {
				if p.Name != project {
					kept = append(kept, p)
				}
			}
			list.Projects = kept
		}); err != nil {
			return nil, err
		}
		return c, nil
	}}, nil
}

// ProjectEntry is a project of ProjectsFile: projects: [{name}], each
// optionally overriding the url, branch or path of the repository the
// projects' template gives it.
type ProjectEntry struct {
	Name   string  `yaml:"name"`
	URL    string  `yaml:"url,omitempty"`
	Branch string  `yaml:"branch,omitempty"`
	Path   *string `yaml:"path,omitempty"`
}

// ProjectsList is ProjectsFile.
type ProjectsList struct {
	Projects []ProjectEntry `yaml:"projects"`
}

// DecodeProjects reads ProjectsFile; an empty file lists none. A name
// that is not a project's (ValidateName), or listed twice, is refused.
func DecodeProjects(raw []byte) (*ProjectsList, error) {
	var list ProjectsList
	if err := yaml.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", ProjectsFile, err)
	}
	seen := map[string]bool{}
	for i, p := range list.Projects {
		if err := ValidateName("project", p.Name); err != nil {
			return nil, fmt.Errorf("%s: projects[%d]: %w", ProjectsFile, i, err)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("%s: project %q is listed twice", ProjectsFile, p.Name)
		}
		seen[p.Name] = true
	}
	return &list, nil
}

// Entry returns the entry of a project, nil when it is not listed.
func (l *ProjectsList) Entry(name string) *ProjectEntry {
	for i := range l.Projects {
		if l.Projects[i].Name == name {
			return &l.Projects[i]
		}
	}
	return nil
}

// updateProjectsList adds to c ProjectsFile as r holds it with change
// applied, its projects by name, when it changes.
func updateProjectsList(r Reader, c *Change, change func(*ProjectsList)) error {
	raw, err := r.ReadFile(ProjectsFile)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	list, err := DecodeProjects(raw)
	if err != nil {
		return err
	}
	change(list)
	sort.Slice(list.Projects, func(i, j int) bool { return list.Projects[i].Name < list.Projects[j].Name })
	if list.Projects == nil {
		list.Projects = []ProjectEntry{}
	}
	data, err := MarshalYAML(list)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, raw) {
		c.write(ProjectsFile, data)
	}
	return nil
}
