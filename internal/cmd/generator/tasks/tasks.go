// The following directive is necessary to make the package coherent:
// This program generates types, It can be invoked by running
// go generate
package main

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strings"
	"text/template"
)

type task struct {
	Name        string   `json:"-"`
	Description string   `json:"description"`
	Input       []string `json:"input,omitempty"`
	Output      []string `json:"output,omitempty"`
}

var funcs = template.FuncMap{
	"lowerFirst": func(s string) string {
		if len(s) == 0 {
			return ""
		}
		if s[0] < 'A' || s[0] > 'Z' {
			return s
		}
		return string(s[0]+'a'-'A') + s[1:]
	},
	"endwith": func(x, y string) bool {
		return strings.HasSuffix(x, y)
	},
	"merge": func(x, y []string) []string {
		a := make(map[string]struct{})
		for _, v := range x {
			a[v] = struct{}{}
		}
		for _, v := range y {
			a[v] = struct{}{}
		}
		o := make([]string, 0)
		for x := range a {
			o = append(o, x)
		}

		sort.Strings(o)
		return o
	},
}

func executeTemplate(tmpl *template.Template, w io.Writer, v interface{}) {
	err := tmpl.Execute(w, v)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	data, err := ioutil.ReadFile("tasks.json")
	if err != nil {
		log.Fatal(err)
	}

	tasks := make(map[string]*task)
	err = json.Unmarshal(data, &tasks)
	if err != nil {
		log.Fatal(err)
	}

	// Do sort to all tasks via name.
	taskNames := make([]string, 0)
	for k := range tasks {
		sort.Strings(tasks[k].Input)
		sort.Strings(tasks[k].Output)

		taskNames = append(taskNames, k)
	}
	sort.Strings(taskNames)

	// Format input tasks.json
	data, err = json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	err = ioutil.WriteFile("tasks.json", data, 0664)
	if err != nil {
		log.Fatal(err)
	}

	taskFile, err := os.Create("generated.go")
	if err != nil {
		log.Fatal(err)
	}
	defer taskFile.Close()

	executeTemplate(taskPageTmpl, taskFile, nil)

	testFile, err := os.Create("generated_test.go")
	if err != nil {
		log.Fatal(err)
	}
	defer testFile.Close()

	executeTemplate(testPageTmpl, testFile, nil)

	for _, taskName := range taskNames {
		v := tasks[taskName]
		v.Name = taskName

		executeTemplate(taskTmpl, taskFile, v)
		executeTemplate(testTmpl, testFile, v)
	}
}

var taskPageTmpl = template.Must(template.New("taskPage").Parse(`// Code generated by go generate; DO NOT EDIT.
package task

import (
	"context"
	"fmt"

	"github.com/Xuanwo/navvy"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/qingstor/noah/pkg/types"
	"github.com/qingstor/noah/pkg/schedule"
)

var _ navvy.Pool
var _ types.Pool
var _ = uuid.New()
`))

var taskTmpl = template.Must(template.New("task").Funcs(funcs).Parse(`
// {{ .Name }}Task will {{ .Description }}.
type {{ .Name }}Task struct {
	// Predefined value
	types.Fault
	types.ID
	types.Pool
	types.Scheduler
	types.CallbackFunc

	// Input value
{{- range $k, $v := .Input }}
	types.{{$v}}
{{- end }}

	// Output value
{{- range $k, $v := .Output }}
	types.{{$v}}
{{- end }}
}

// New{{ .Name }} will create a {{ .Name }}Task struct and fetch inherited data from parent task.
func New{{ .Name }}(task navvy.Task) *{{ .Name }}Task {
	t := &{{ .Name }}Task{}
	t.SetID(uuid.New().String())

	t.loadInput(task)
	t.SetScheduler(schedule.NewScheduler(t.GetPool()))

	t.new()
	return t
}

// validateInput will validate all input before run task.
func (t *{{ .Name }}Task) validateInput() {
{{- range $k, $v := .Input }}
	if !t.Validate{{$v}}() {
		panic(fmt.Errorf("Task {{ $.Name }} value {{$v}} is invalid"))
	}
{{- end }}
}

// loadInput will check and load all input before new task.
func (t *{{ .Name }}Task) loadInput(task navvy.Task) {
	types.LoadFault(task, t)
	types.LoadPool(task, t)

{{- range $k, $v := .Input }}
	types.Load{{$v}}(task, t)
{{- end }}
}

// Run implement navvy.Task
func (t *{{ .Name }}Task) Run(ctx context.Context) {
	t.validateInput()

	log.Debugf("Started %s", t)
	t.run(ctx)
	t.GetScheduler().Wait()
	if t.GetFault().HasError() {
		log.Debugf("Finished %s with error [%s]", t, t.GetFault().Error())
		return
	}
	if t.ValidateCallbackFunc() {
		t.GetCallbackFunc()()
	}
	log.Debugf("Finished %s", t)
}

// Context implement navvy.Task
func (t *{{ .Name }}Task) Context() context.Context {
	return context.TODO()
}

// TriggerFault will be used to trigger a task related fault.
func (t *{{ .Name }}Task) TriggerFault(err error) {
	t.GetFault().Append(fmt.Errorf("Failed %s: {%w}",t , err))
}

// String will implement Stringer interface.
func (t *{{ .Name }}Task) String() string {
	return fmt.Sprintf("{{ .Name }}Task {
{{- $called := false -}}
{{- range $k, $v := .Input -}}
{{- if not (endwith $v "Func") -}}
	{{ if $called }}, {{end}}{{$v}}: %v
	{{- $called = true -}}
{{- end -}}
{{- end -}}
}",
{{- $called := false -}}
{{- range $k, $v := .Input -}}
{{- if not (endwith $v "Func") -}}
	{{ if $called }}, {{end}}t.Get{{$v}}()
	{{- $called = true -}}
{{- end -}}
{{- end -}})
}



// New{{ .Name }}Task will create a {{ .Name }}Task which meets navvy.Task.
func New{{ .Name }}Task(task navvy.Task) navvy.Task {
	return New{{ .Name }}(task)
}
`))

var testPageTmpl = template.Must(template.New("testPage").Parse(`// Code generated by go generate; DO NOT EDIT.
package task

import (
	"context"
	"errors"
	"testing"

	"github.com/Xuanwo/navvy"
	"github.com/stretchr/testify/assert"

	"github.com/qingstor/noah/pkg/types"
	"github.com/qingstor/noah/pkg/fault"
)

var _ navvy.Pool
var _ types.Pool
`))

var testTmpl = template.Must(template.New("test").Funcs(funcs).Parse(`
func Test{{ .Name }}Task_TriggerFault(t *testing.T) {
	task := &{{ .Name }}Task{}
	task.SetFault(fault.New())
	err := errors.New("test error")
	task.TriggerFault(err)
	assert.True(t, task.GetFault().HasError())
}
`))
