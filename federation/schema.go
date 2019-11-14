package federation

import (
	"errors"
	"fmt"

	"github.com/samsarahq/thunder/graphql"
)

type FieldInfo struct {
	Service  string
	Services map[string]bool
}

type SchemaWithFederationInfo struct {
	Schema *graphql.Schema
	Fields map[*graphql.Field]*FieldInfo
}

type introspectionTypeRef struct {
	Kind   string                `json:"kind"`
	Name   string                `json:"name"`
	OfType *introspectionTypeRef `json:"ofType"`
}

type introspectionQueryResult struct {
	Schema struct {
		Types []struct {
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			Fields []struct {
				Name string                `json:"name"`
				Type *introspectionTypeRef `json:"type"`
			} `json:"fields"`
			PossibleTypes []*introspectionTypeRef `json:"possibleTypes"`
		} `json:"types"`
	} `json:"__schema"`
}

func convertSchema(schemas map[string]introspectionQueryResult) (*SchemaWithFederationInfo, error) {
	byName := make(map[string]*graphql.Object)
	all := make(map[string]graphql.Type)
	fieldInfos := make(map[*graphql.Field]*FieldInfo)

	for _, schema := range schemas {
		for _, typ := range schema.Schema.Types {
			switch typ.Kind {
			case "OBJECT":
				if _, ok := byName[typ.Name]; !ok {
					byName[typ.Name] = &graphql.Object{
						Name:   typ.Name,
						Fields: make(map[string]*graphql.Field),
					}
					all[typ.Name] = byName[typ.Name]
				}

			case "SCALAR":
				all[typ.Name] = &graphql.Scalar{
					Type: typ.Name,
				}

			case "UNION":
				all[typ.Name] = &graphql.Union{
					Name:  typ.Name,
					Types: make(map[string]*graphql.Object),
				}

			default:
				return nil, fmt.Errorf("unknown type kind %s", typ.Kind)
			}
		}
	}

	var convert func(*introspectionTypeRef) (graphql.Type, error)
	convert = func(t *introspectionTypeRef) (graphql.Type, error) {
		if t == nil {
			return nil, errors.New("malformed typeref")
		}

		switch t.Kind {
		case "SCALAR", "OBJECT", "UNION":
			// XXX: enforce type?
			typ, ok := all[t.Name]
			if !ok {
				return nil, fmt.Errorf("type %s not found among top-level types", t.Name)
			}
			return typ, nil
		case "LIST", "NON_NULL":
			inner, err := convert(t.OfType)
			if err != nil {
				return nil, err
			}
			if t.Kind == "LIST" {
				return &graphql.List{
					Type: inner,
				}, nil
			} else {
				return &graphql.NonNull{
					Type: inner,
				}, nil
			}

			// xxx: ban duplicates so we can guarantee types below are same

		default:
			return nil, fmt.Errorf("unknown type kind %s", t.Kind)
		}
	}

	for service, schema := range schemas {
		for _, typ := range schema.Schema.Types {
			switch typ.Kind {
			case "OBJECT":
				obj := byName[typ.Name]

				for _, field := range typ.Fields {
					f, ok := obj.Fields[field.Name]
					if !ok {
						typ, err := convert(field.Type)
						if err != nil {
							return nil, fmt.Errorf("service %s typ %s field %s has bad typ: %v",
								service, typ, field.Name, err)
						}

						f = &graphql.Field{
							Args: nil, // xxx
							Type: typ, // XXX
						}
						obj.Fields[field.Name] = f
						fieldInfos[f] = &FieldInfo{
							Service:  service,
							Services: map[string]bool{},
						}
					}

					// XXX check consistent types

					fieldInfos[f].Services[service] = true
				}

			case "UNION":
				union := all[typ.Name].(*graphql.Union)
				for _, other := range typ.PossibleTypes {
					if other.Kind != "OBJECT" {
						return nil, fmt.Errorf("service %s typ %s has possible typ not OBJECT: %v", service, typ.Name, other)
					}
					typ, ok := all[other.Name].(*graphql.Object)
					if !ok {
						return nil, fmt.Errorf("service %s typ %s possible typ %s does not refer to obj", service, typ.Name, other.Name)
					}
					union.Types[typ.Name] = typ
				}
			}
		}
	}

	return &SchemaWithFederationInfo{
		Schema: &graphql.Schema{
			Query:    byName["Query"],    // XXX
			Mutation: byName["Mutation"], // XXX
		},
		Fields: fieldInfos,
	}, nil
}

// schema.Extend()

// XXX: any types you return you must have the definition for...
//
//   how do we enforce that?? some compile time check that crosses package
//   boundaries and spots Object() (or whatever) calls that are automatic in some
//   package and not in another?
//
//   could not do magic anymore and require an explicit "schema.Object" call for
//   any types returned... maybe with schema.AutoObject("") to handle automatic
//   cases?
//
// XXX: could not allow schemabuilder auto objects outside of packages? seems nice.
// }