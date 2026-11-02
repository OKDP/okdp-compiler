package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	compiler "github.com/okdp/okdp-compiler"
)

// The statuses of an instance in a report (instanceReport.Status).
const (
	// statusCompiled: compiled, and the deployment graph has no problem at
	// its release.
	statusCompiled = "compiled"
	// statusRefused: its compile failed, or the graph has a problem at its
	// release: instanceReport.Error says why.
	statusRefused = "refused"
	// statusSkipped: not compiled, the compile stopped before it was done
	// (--timeout); or, with --partial, compiled but not written, another
	// instance of its project refused.
	statusSkipped = "skipped"
)

// report: what --report writes, as JSON: every instance of the tree, in
// the order the command compiles them, and the errors of the tree that
// are no instance's (an option, a repository, the target, the output,
// --timeout); written, whether the files of the target were written.
type report struct {
	Instances []*instanceReport `json:"instances"`
	Errors    []string          `json:"errors"`
	Written   bool              `json:"written"`
}

// instanceReport: an instance of a report. Service and Version are its
// package's; Privileged its result's (false when refused); Error, its
// refusal, "" when none; the lists empty, never null.
type instanceReport struct {
	Namespace    string                `json:"namespace"`
	Instance     string                `json:"instance"`
	Release      string                `json:"release"`
	Service      string                `json:"service"`
	Version      string                `json:"version"`
	Privileged   bool                  `json:"privileged"`
	Status       string                `json:"status"`
	Error        string                `json:"error"`
	Warnings     []string              `json:"warnings"`
	Dependencies []compiler.Dependency `json:"dependencies"`
	Claims       []claimReport         `json:"claims"`
	Charts       []chartReport         `json:"charts"`
}

// claimReport: a claim of a reference of the instance, bound to its
// provider (compiler.Bound): its class, "" when the claim names its
// provider.
type claimReport struct {
	Field    string       `json:"field"`
	ID       string       `json:"id"`
	Contract string       `json:"contract"`
	Provider providerName `json:"provider"`
	Class    string       `json:"class"`
}

// providerName: the instance a claim is bound to.
type providerName struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
}

// chartReport: an upstream chart the instance renders (Result.Charts), by
// its key in the umbrella chart.
type chartReport struct {
	Key        string `json:"key"`
	Repository string `json:"repository"`
	Chart      string `json:"chart"`
	Version    string `json:"version"`
}

// newReport: a report of nothing yet.
func newReport() *report {
	return &report{Instances: []*instanceReport{}, Errors: []string{}}
}

// add: the instance in, its outcome out, statusCompiled or statusRefused.
func (r *report) add(in compiler.Instance, out compiler.Outcome) {
	ir := r.instance(in)
	for _, b := range out.Claims {
		ir.Claims = append(ir.Claims, claimReport{Field: b.Field, ID: b.ID, Contract: b.Contract, Provider: providerName{b.Namespace, b.Release}, Class: b.Class})
	}
	if out.Err != nil {
		ir.Status, ir.Error = statusRefused, out.Err.Error()
		return
	}
	res := out.Result
	ir.Status = statusCompiled
	ir.Service, ir.Version, ir.Privileged = res.Service, res.Version, res.Privileged
	ir.Warnings = append(ir.Warnings, res.Warnings...)
	ir.Dependencies = append(ir.Dependencies, res.Dependencies...)
	for _, key := range slices.Sorted(maps.Keys(res.Charts)) {
		ref := res.Charts[key]
		ir.Charts = append(ir.Charts, chartReport{Key: key, Repository: ref.Repository, Chart: ref.Chart, Version: ref.Version})
	}
}

// notWritten: the instances of namespace that compiled statusSkipped:
// their project is not written (--partial).
func (r *report) notWritten(namespace string) {
	for _, ir := range r.Instances {
		if ir.Namespace == namespace && ir.Status == statusCompiled {
			ir.Status = statusSkipped
		}
	}
}

// skip: the instance in, statusSkipped.
func (r *report) skip(in compiler.Instance) { r.instance(in).Status = statusSkipped }

// instance: the record of in, added, its package's service and version.
func (r *report) instance(in compiler.Instance) *instanceReport {
	ir := &instanceReport{Namespace: in.Inputs.Namespace, Instance: in.Name, Release: in.Inputs.Release, Warnings: []string{}, Dependencies: []compiler.Dependency{}, Claims: []claimReport{}, Charts: []chartReport{}}
	if in.Definition != nil {
		ir.Service, ir.Version = in.Definition.Chart.Name, in.Definition.Chart.Version
	}
	r.Instances = append(r.Instances, ir)
	return ir
}

// graphError: a problem of the deployment graph, the instance of its
// release refused, its error joined to any other; a tree error when no
// instance has the release.
func (r *report) graphError(err error) {
	if ge, ok := errors.AsType[*compiler.GraphError](err); ok {
		for _, ir := range r.Instances {
			if ir.Namespace == ge.Node.Namespace && ir.Release == ge.Node.Release {
				ir.Status = statusRefused
				ir.Error = joinMessages(ir.Error, ge.Err.Error())
				return
			}
		}
	}
	r.Errors = append(r.Errors, err.Error())
}

// joinMessages: a and b on two lines, either alone when the other is "".
func joinMessages(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

// write: the report as JSON, indented, to file, or stdout for -; err, what
// the command fails with, a tree error unless printed already
// (errReported).
func (r *report) write(file string, err error) error {
	if err != nil && !errors.Is(err, errReported) {
		r.Errors = append(r.Errors, err.Error())
	}
	b, merr := json.MarshalIndent(r, "", "  ")
	if merr != nil {
		return merr
	}
	b = append(b, '\n')
	if file == "-" {
		_, merr = os.Stdout.Write(b)
	} else {
		merr = os.WriteFile(file, b, 0o644)
	}
	if merr != nil {
		return fmt.Errorf("--report: %w", merr)
	}
	return nil
}
