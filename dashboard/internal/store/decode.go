package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func decode(body []byte) (snapshot, bool) {
	if !utf8.Valid(body) {
		return snapshot{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return snapshot{}, false
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return snapshot{}, false
	}
	obj, ok := asMap(raw)
	if !ok {
		return snapshot{}, false
	}
	return snapshotFrom(obj)
}

func snapshotFrom(obj map[string]any) (snapshot, bool) {
	schema, ok := uintFrom(obj["schema"])
	if !ok || schema != 1 {
		return snapshot{}, false
	}
	serverID, ok := stringFrom(obj["server_id"])
	if !ok || !validServerID(serverID) {
		return snapshot{}, false
	}
	live, ok := boolFrom(obj["live"])
	if !ok {
		return snapshot{}, false
	}
	ready, ok := boolFrom(obj["ready"])
	if !ok {
		return snapshot{}, false
	}
	connected, ok := boolFrom(obj["nats_connected"])
	if !ok {
		return snapshot{}, false
	}
	microservice, ok := optionalString(obj, "microservice_name")
	if !ok {
		return snapshot{}, false
	}
	node, ok := optionalString(obj, "node_uid")
	if !ok {
		return snapshot{}, false
	}
	application, ok := optionalString(obj, "application_name")
	if !ok {
		return snapshot{}, false
	}
	providers, ok := optionalStrings(obj, "execution_providers")
	if !ok {
		return snapshot{}, false
	}
	ortVersion, ok := optionalString(obj, "ort_version")
	if !ok {
		return snapshot{}, false
	}
	models, ok := decodeModels(obj["models"])
	if !ok {
		return snapshot{}, false
	}
	return snapshot{
		serverID:           serverID,
		microserviceName:   microservice,
		nodeUID:            node,
		applicationName:    application,
		live:               live,
		ready:              ready,
		natsConnected:      connected,
		executionProviders: providers,
		ortVersion:         ortVersion,
		models:             models,
	}, true
}

func decodeModels(v any) ([]Model, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	models := make([]Model, 0, len(items))
	for _, item := range items {
		obj, objOK := asMap(item)
		if !objOK {
			return nil, false
		}
		model, modelOK := decodeModel(obj)
		if !modelOK {
			return nil, false
		}
		models = append(models, model)
	}
	sortModels(models)
	return models, true
}

func decodeModel(obj map[string]any) (Model, bool) {
	name, ok := requiredString(obj, "model")
	if !ok {
		return Model{}, false
	}
	version, ok := requiredString(obj, "version")
	if !ok {
		return Model{}, false
	}
	state, ok := requiredString(obj, "state")
	if !ok || !validState(state) {
		return Model{}, false
	}
	reason, ok := requiredString(obj, "reason")
	if !ok {
		return Model{}, false
	}
	success, ok := requiredUint(obj, "inference_request_success")
	if !ok {
		return Model{}, false
	}
	failure, ok := decodeFailure(obj["inference_request_failure"])
	if !ok {
		return Model{}, false
	}
	count, ok := requiredUint(obj, "inference_count")
	if !ok {
		return Model{}, false
	}
	exec, ok := requiredUint(obj, "inference_exec_count")
	if !ok {
		return Model{}, false
	}
	pending, ok := requiredUint(obj, "inference_pending_request_count")
	if !ok {
		return Model{}, false
	}
	request, ok := requiredUint(obj, "inference_request_duration_us")
	if !ok {
		return Model{}, false
	}
	queue, ok := requiredUint(obj, "inference_queue_duration_us")
	if !ok {
		return Model{}, false
	}
	compute, ok := requiredUint(obj, "inference_compute_infer_duration_us")
	if !ok {
		return Model{}, false
	}
	ready, ok := requiredUint(obj, "inference_model_ready")
	if !ok {
		return Model{}, false
	}
	return Model{
		Name:            name,
		Version:         version,
		State:           state,
		Reason:          reason,
		Success:         success,
		Failure:         failure,
		Count:           count,
		ExecCount:       exec,
		Pending:         pending,
		RequestDuration: request,
		QueueDuration:   queue,
		ComputeDuration: compute,
		ModelReady:      ready,
	}, true
}

func decodeFailure(v any) (Failure, bool) {
	obj, ok := asMap(v)
	if !ok {
		return Failure{}, false
	}
	rejected, ok := requiredUint(obj, "REJECTED")
	if !ok {
		return Failure{}, false
	}
	canceled, ok := requiredUint(obj, "CANCELED")
	if !ok {
		return Failure{}, false
	}
	backend, ok := requiredUint(obj, "BACKEND")
	if !ok {
		return Failure{}, false
	}
	other, ok := requiredUint(obj, "OTHER")
	if !ok {
		return Failure{}, false
	}
	return Failure{Rejected: rejected, Canceled: canceled, Backend: backend, Other: other}, true
}

func sortModels(models []Model) {
	slices.SortStableFunc(models, func(a, b Model) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return compareVersion(a.Version, b.Version)
	})
}

func compareVersion(a, b string) int {
	ai, aerr := strconv.ParseUint(a, 10, 64)
	bi, berr := strconv.ParseUint(b, 10, 64)
	if aerr == nil && berr == nil && ai != bi {
		if ai < bi {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func validState(state string) bool {
	switch state {
	case StateReady, StateLoading, StateUnavailable:
		return true
	default:
		return false
	}
}

func validServerID(id string) bool {
	if id == "" {
		return false
	}
	return !strings.ContainsAny(id, ".*/>")
}

func optionalStrings(obj map[string]any, key string) ([]string, bool) {
	v, present := obj[key]
	if !present || v == nil {
		return nil, true
	}
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, textOK := stringFrom(item)
		if !textOK {
			return nil, false
		}
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out, true
}

func optionalString(obj map[string]any, key string) (string, bool) {
	v, present := obj[key]
	if !present {
		return "", true
	}
	text, ok := stringFrom(v)
	if !ok {
		return "", false
	}
	return text, true
}

func requiredString(obj map[string]any, key string) (string, bool) {
	v, present := obj[key]
	if !present {
		return "", false
	}
	return stringFrom(v)
}

func requiredUint(obj map[string]any, key string) (uint64, bool) {
	v, present := obj[key]
	if !present {
		return 0, false
	}
	return uintFrom(v)
}

func uintFrom(v any) (uint64, bool) {
	number, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	text := number.String()
	if text == "" || text[0] == '-' {
		return 0, false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func stringFrom(v any) (string, bool) {
	text, ok := v.(string)
	return text, ok
}

func boolFrom(v any) (bool, bool) {
	bit, ok := v.(bool)
	return bit, ok
}

func asMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}
