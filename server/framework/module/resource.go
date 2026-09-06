package module

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// OwnershipKind describes the database scope a generated resource must use.
// It is metadata for generators and composition checks, not a reflection-based
// runtime authorization shortcut.
type OwnershipKind string

const (
	OwnershipSystem OwnershipKind = "system"
	OwnershipOwner  OwnershipKind = "owner"
	OwnershipCustom OwnershipKind = "custom"
)

// ResourceOperationDefinition binds one registered external operation to its
// request and response DTOs. Nil DTOs explicitly mean no body in that
// direction, for example a DELETE with a 204 response.
type ResourceOperationDefinition struct {
	OperationID string
	RequestDTO  reflect.Type
	ResponseDTO reflect.Type
}

// ResourceDefinition is build-time metadata consumed by resource generators.
// Runtime CRUD generation is deliberately outside this contract.
//
// AuditableFields and SensitiveFields must both be non-nil, even when empty,
// so a module cannot accidentally omit its data-classification decision.
type ResourceDefinition struct {
	Name            string
	Model           reflect.Type
	Ownership       OwnershipKind
	Policy          PolicyRef
	AuditableFields []string
	SensitiveFields []string
	Operations      []ResourceOperationDefinition
}

var (
	resourceNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	resourceFieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

func validateResource(definition ResourceDefinition) (ResourceDefinition, error) {
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Policy = PolicyRef(strings.TrimSpace(string(definition.Policy)))
	if !resourceNamePattern.MatchString(definition.Name) {
		return ResourceDefinition{}, fmt.Errorf(
			"%w: resource name %q",
			ErrInvalid,
			definition.Name,
		)
	}
	model := definition.Model
	if model == nil {
		return ResourceDefinition{}, fmt.Errorf(
			"%w: resource %q has no model",
			ErrInvalid,
			definition.Name,
		)
	}
	for model.Kind() == reflect.Pointer {
		model = model.Elem()
	}
	if model.Kind() != reflect.Struct {
		return ResourceDefinition{}, fmt.Errorf(
			"%w: resource %q model must be a struct",
			ErrInvalid,
			definition.Name,
		)
	}
	definition.Model = model

	switch definition.Ownership {
	case OwnershipSystem:
		if definition.Policy != PolicyRef("system") {
			return ResourceDefinition{}, fmt.Errorf(
				"%w: system resource %q must use the system policy",
				ErrInvalid,
				definition.Name,
			)
		}
	case OwnershipOwner:
		if definition.Policy != PolicyRef("owner") {
			return ResourceDefinition{}, fmt.Errorf(
				"%w: owner resource %q must use the owner policy",
				ErrInvalid,
				definition.Name,
			)
		}
	case OwnershipCustom:
		if !validName(string(definition.Policy)) {
			return ResourceDefinition{}, fmt.Errorf(
				"%w: custom resource %q must declare a policy",
				ErrInvalid,
				definition.Name,
			)
		}
	default:
		return ResourceDefinition{}, fmt.Errorf(
			"%w: resource %q has invalid ownership %q",
			ErrInvalid,
			definition.Name,
			definition.Ownership,
		)
	}

	if definition.AuditableFields == nil || definition.SensitiveFields == nil {
		return ResourceDefinition{}, fmt.Errorf(
			"%w: resource %q must explicitly classify audit and sensitive fields",
			ErrInvalid,
			definition.Name,
		)
	}
	auditable, err := normalizeResourceFields(
		definition.Name,
		"auditable",
		definition.AuditableFields,
		nil,
	)
	if err != nil {
		return ResourceDefinition{}, err
	}
	auditableSet := make(map[string]struct{}, len(auditable))
	for _, field := range auditable {
		auditableSet[field] = struct{}{}
	}
	sensitive, err := normalizeResourceFields(
		definition.Name,
		"sensitive",
		definition.SensitiveFields,
		auditableSet,
	)
	if err != nil {
		return ResourceDefinition{}, err
	}
	definition.AuditableFields = auditable
	definition.SensitiveFields = sensitive

	if len(definition.Operations) == 0 {
		return ResourceDefinition{}, fmt.Errorf(
			"%w: resource %q has no external operations",
			ErrInvalid,
			definition.Name,
		)
	}
	operations := append(
		[]ResourceOperationDefinition(nil),
		definition.Operations...,
	)
	seen := make(map[string]struct{}, len(operations))
	for index := range operations {
		operation := &operations[index]
		operation.OperationID = strings.TrimSpace(operation.OperationID)
		if !operationIDPattern.MatchString(operation.OperationID) {
			return ResourceDefinition{}, fmt.Errorf(
				"%w: resource %q has invalid operation %q",
				ErrInvalid,
				definition.Name,
				operation.OperationID,
			)
		}
		if _, exists := seen[operation.OperationID]; exists {
			return ResourceDefinition{}, fmt.Errorf(
				"%w: resource %q repeats operation %q",
				ErrDuplicate,
				definition.Name,
				operation.OperationID,
			)
		}
		seen[operation.OperationID] = struct{}{}
		if err := validateResourceDTO(
			definition.Name,
			operation.OperationID,
			"request",
			operation.RequestDTO,
			model,
		); err != nil {
			return ResourceDefinition{}, err
		}
		if err := validateResourceDTO(
			definition.Name,
			operation.OperationID,
			"response",
			operation.ResponseDTO,
			model,
		); err != nil {
			return ResourceDefinition{}, err
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].OperationID < operations[j].OperationID
	})
	definition.Operations = operations
	return definition, nil
}

func normalizeResourceFields(
	resource string,
	classification string,
	fields []string,
	disallowed map[string]struct{},
) ([]string, error) {
	result := append([]string(nil), fields...)
	seen := make(map[string]struct{}, len(result))
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
		field := result[index]
		if !resourceFieldPattern.MatchString(field) {
			return nil, fmt.Errorf(
				"%w: resource %q has invalid %s field %q",
				ErrInvalid,
				resource,
				classification,
				field,
			)
		}
		if _, exists := seen[field]; exists {
			return nil, fmt.Errorf(
				"%w: resource %q repeats %s field %q",
				ErrDuplicate,
				resource,
				classification,
				field,
			)
		}
		if _, unsafe := disallowed[field]; unsafe {
			return nil, fmt.Errorf(
				"%w: resource %q classifies sensitive field %q as auditable",
				ErrInvalid,
				resource,
				field,
			)
		}
		seen[field] = struct{}{}
	}
	sort.Strings(result)
	return result, nil
}

func validateResourceDTO(
	resource string,
	operation string,
	direction string,
	dto reflect.Type,
	model reflect.Type,
) error {
	if dto == nil {
		return nil
	}
	for dto.Kind() == reflect.Pointer {
		dto = dto.Elem()
	}
	if dto.Kind() != reflect.Struct {
		return fmt.Errorf(
			"%w: resource %q operation %q %s DTO must be a struct",
			ErrInvalid,
			resource,
			operation,
			direction,
		)
	}
	if dto == model {
		return fmt.Errorf(
			"%w: resource %q operation %q reuses its database model as a %s DTO",
			ErrInvalid,
			resource,
			operation,
			direction,
		)
	}
	return nil
}

func cloneResource(definition ResourceDefinition) ResourceDefinition {
	cloned := definition
	cloned.AuditableFields = append([]string(nil), definition.AuditableFields...)
	cloned.SensitiveFields = append([]string(nil), definition.SensitiveFields...)
	cloned.Operations = append(
		[]ResourceOperationDefinition(nil),
		definition.Operations...,
	)
	return cloned
}
