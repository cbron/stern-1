//   Copyright 2016 Wercker Holding BV
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"text/template"
	"time"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/stern/stern/stern"

	"github.com/fatih/color"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

type Options struct {
	container        string
	excludeContainer string
	containerState   []string
	timestamps       bool
	since            time.Duration
	context          string
	namespaces       []string
	kubeConfig       string
	exclude          []string
	include          []string
	initContainers   bool
	allNamespaces    bool
	selector         string
	fieldSelector    string
	tail             int64
	color            string
	version          bool
	completion       string
	template         string
	output           string
}

var opts = &Options{
	container:      ".*",
	containerState: []string{stern.RUNNING},
	initContainers: true,
	tail:           -1,
	color:          "auto",
	template:       "",
	output:         "default",
}

func Run() {
	cmd := &cobra.Command{}
	cmd.Use = "stern pod-query"
	cmd.Short = "Tail multiple pods and containers from Kubernetes"

	cmd.Flags().StringVarP(&opts.container, "container", "c", opts.container, "Container name when multiple containers in pod")
	cmd.Flags().StringVarP(&opts.excludeContainer, "exclude-container", "E", opts.excludeContainer, "Exclude a Container name")
	cmd.Flags().StringSliceVar(&opts.containerState, "container-state", opts.containerState, "If present, tail containers with status in running, waiting or terminated. Defaults to running.")
	cmd.Flags().BoolVarP(&opts.timestamps, "timestamps", "t", opts.timestamps, "Print timestamps")
	cmd.Flags().DurationVarP(&opts.since, "since", "s", opts.since, "Return logs newer than a relative duration like 5s, 2m, or 3h. Defaults to 48h.")
	cmd.Flags().StringVar(&opts.context, "context", opts.context, "Kubernetes context to use. Default to current context configured in kubeconfig.")
	cmd.Flags().StringSliceVarP(&opts.namespaces, "namespace", "n", opts.namespaces, "Kubernetes namespace to use. Default to namespace configured in Kubernetes context. To specify multiple namespaces, repeat this or set comma-separated value.")
	cmd.Flags().StringVar(&opts.kubeConfig, "kubeconfig", opts.kubeConfig, "Path to kubeconfig file to use")
	cmd.Flags().StringVar(&opts.kubeConfig, "kube-config", opts.kubeConfig, "Path to kubeconfig file to use")
	_ = cmd.Flags().MarkDeprecated("kube-config", "Use --kubeconfig instead.")
	cmd.Flags().StringSliceVarP(&opts.exclude, "exclude", "e", opts.exclude, "Regex of log lines to exclude")
	cmd.Flags().StringSliceVarP(&opts.include, "include", "i", opts.include, "Regex of log lines to include")
	cmd.Flags().BoolVar(&opts.initContainers, "init-containers", opts.initContainers, "Include or exclude init containers")
	cmd.Flags().BoolVarP(&opts.allNamespaces, "all-namespaces", "A", opts.allNamespaces, "If present, tail across all namespaces. A specific namespace is ignored even if specified with --namespace.")
	cmd.Flags().StringVarP(&opts.selector, "selector", "l", opts.selector, "Selector (label query) to filter on. If present, default to \".*\" for the pod-query.")
	cmd.Flags().StringVar(&opts.fieldSelector, "field-selector", opts.fieldSelector, "Selector (field query) to filter on. If present, default to \".*\" for the pod-query.")
	cmd.Flags().Int64Var(&opts.tail, "tail", opts.tail, "The number of lines from the end of the logs to show. Defaults to -1, showing all logs.")
	cmd.Flags().StringVar(&opts.color, "color", opts.color, "Color output. Can be 'always', 'never', or 'auto'")
	cmd.Flags().BoolVarP(&opts.version, "version", "v", opts.version, "Print the version and exit")
	cmd.Flags().StringVar(&opts.completion, "completion", opts.completion, "Outputs stern command-line completion code for the specified shell. Can be 'bash' or 'zsh'")
	cmd.Flags().StringVar(&opts.template, "template", opts.template, "Template to use for log lines, leave empty to use --output flag")
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Specify predefined template. Currently support: [default, raw, json]")

	// Specify custom bash completion function
	cmd.BashCompletionFunction = bash_completion_func
	for name, completion := range bash_completion_flags {
		if cmd.Flag(name) != nil {
			if cmd.Flag(name).Annotations == nil {
				cmd.Flag(name).Annotations = map[string][]string{}
			}
			cmd.Flag(name).Annotations[cobra.BashCompCustom] = append(
				cmd.Flag(name).Annotations[cobra.BashCompCustom],
				completion,
			)
		}
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if opts.version {
			fmt.Println(buildVersion(version, commit, date))
			return nil
		}

		if opts.completion != "" {
			return runCompletion(opts.completion, cmd)
		}

		narg := len(args)
		if (narg > 1) || (narg == 0 && opts.selector == "" && opts.fieldSelector == "") {
			return cmd.Help()
		}
		config, err := parseConfig(args)
		if err != nil {
			log.Println(err)
			os.Exit(2)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = stern.Run(ctx, config)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		return nil
	}

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func parseConfig(args []string) (*stern.Config, error) {
	var podQuery string
	if len(args) == 0 {
		podQuery = ".*"
	} else {
		podQuery = args[0]
	}
	pod, err := regexp.Compile(podQuery)
	if err != nil {
		return nil, errors.Wrap(err, "failed to compile regular expression from query")
	}

	container, err := regexp.Compile(opts.container)
	if err != nil {
		return nil, errors.Wrap(err, "failed to compile regular expression for container query")
	}

	var excludeContainer *regexp.Regexp
	if opts.excludeContainer != "" {
		excludeContainer, err = regexp.Compile(opts.excludeContainer)
		if err != nil {
			return nil, errors.Wrap(err, "failed to compile regular expression for exclude container query")
		}
	}

	var exclude []*regexp.Regexp
	for _, ex := range opts.exclude {
		rex, err := regexp.Compile(ex)
		if err != nil {
			return nil, errors.Wrap(err, "failed to compile regular expression for exclusion filter")
		}

		exclude = append(exclude, rex)
	}

	var include []*regexp.Regexp
	for _, inc := range opts.include {
		rin, err := regexp.Compile(inc)
		if err != nil {
			return nil, errors.Wrap(err, "failed to compile regular expression for inclusion filter")
		}

		include = append(include, rin)
	}

	containerState, err := stern.NewContainerState(opts.containerState)
	if err != nil {
		return nil, err
	}

	var labelSelector labels.Selector
	if opts.selector == "" {
		labelSelector = labels.Everything()
	} else {
		labelSelector, err = labels.Parse(opts.selector)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse selector as label selector")
		}
	}

	var fieldSelector fields.Selector
	if opts.fieldSelector == "" {
		fieldSelector = fields.Everything()
	} else {
		fieldSelector, err = fields.ParseSelector(opts.fieldSelector)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse selector as field selector")
		}
	}

	var tailLines *int64
	if opts.tail != -1 {
		tailLines = &opts.tail
	}

	colorFlag := opts.color
	if colorFlag == "always" {
		color.NoColor = false
	} else if colorFlag == "never" {
		color.NoColor = true
	} else if colorFlag != "auto" {
		return nil, errors.New("color should be one of 'always', 'never', or 'auto'")
	}

	t := opts.template
	if t == "" {
		switch opts.output {
		case "default":
			if color.NoColor {
				t = "{{.PodName}} {{.ContainerName}} {{.Message}}"
				if opts.allNamespaces || len(opts.namespaces) > 1 {
					t = fmt.Sprintf("{{.Namespace}} %s", t)
				}
			} else {
				t = "{{color .PodColor .PodName}} {{color .ContainerColor .ContainerName}} {{.Message}}"
				if opts.allNamespaces || len(opts.namespaces) > 1 {
					t = fmt.Sprintf("{{color .PodColor .Namespace}} %s", t)
				}

			}
		case "raw":
			t = "{{.Message}}"
		case "json":
			t = "{{json .}}"
		}
		t += "\n"
	}

	funs := map[string]interface{}{
		"json": func(in interface{}) (string, error) {
			b, err := json.Marshal(in)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		"color": func(color color.Color, text string) string {
			return color.SprintFunc()(text)
		},
	}
	template, err := template.New("log").Funcs(funs).Parse(t)
	if err != nil {
		return nil, errors.Wrap(err, "unable to parse template")
	}

	if opts.since == 0 {
		opts.since = 172800000000000 // 48h
	}

	// Make namespaces array unique
	namespaces := []string{}
	if opts.namespaces != nil {
		m := make(map[string]struct{})
		for _, namespace := range opts.namespaces {
			if namespace == "" {
				continue
			}

			if _, ok := m[namespace]; !ok {
				m[namespace] = struct{}{}
				namespaces = append(namespaces, namespace)
			}
		}
	}

	return &stern.Config{
		KubeConfig:            opts.kubeConfig,
		PodQuery:              pod,
		ContainerQuery:        container,
		ExcludeContainerQuery: excludeContainer,
		ContainerState:        containerState,
		Exclude:               exclude,
		Include:               include,
		Timestamps:            opts.timestamps,
		Since:                 opts.since,
		ContextName:           opts.context,
		Namespaces:            namespaces,
		AllNamespaces:         opts.allNamespaces,
		LabelSelector:         labelSelector,
		FieldSelector:         fieldSelector,
		TailLines:             tailLines,
		Template:              template,
	}, nil
}

func buildVersion(version, commit, date string) string {
	result := fmt.Sprintf("version: %s", version)

	if commit != "" {
		result = fmt.Sprintf("%s\ncommit: %s", result, commit)
	}

	if date != "" {
		result = fmt.Sprintf("%s\nbuilt at: %s", result, date)
	}

	return result
}
