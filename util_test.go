package graphql_test

import (
	"encoding/json"
	"log"
	"reflect"
	"testing"
	"time"

	"github.com/IodeSystems/graphql-go/v2"
	"github.com/IodeSystems/graphql-go/v2/testutil"
)

type Person struct {
	Human
	Name    string   `json:"name"`
	Home    Address  `json:"home"`
	Hobbies []string `json:"hobbies"`
	Friends []Friend `json:"friends"`
}

type Human struct {
	Alive  bool      `json:"alive,omitempty"`
	Age    int       `json:"age"`
	Weight float64   `json:"weight"`
	DoB    time.Time `json:"dob"`
}

type Friend struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type Address struct {
	Street string `json:"street"`
	City   string `json:"city"`
	Test   string `json:",omitempty"`
}

var personSource = Person{
	Human: Human{
		Age:    24,
		Weight: 70.1,
		Alive:  true,
		DoB:    time.Date(2019, 01, 01, 01, 01, 01, 0, time.UTC),
	},
	Name: "John Doe",
	Home: Address{
		Street: "Jl. G1",
		City:   "Jakarta",
	},
	Friends: friendSource,
	Hobbies: []string{"eat", "sleep", "code"},
}

var friendSource = []Friend{
	{Name: "Arief", Address: "palembang"},
	{Name: "Al", Address: "semarang"},
}

func TestBindFields(t *testing.T) {
	// create person type based on Person struct
	personType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Person",
		// pass empty Person struct to bind all of it's fields
		Fields: graphql.BindFields(Person{}),
	})
	fields := graphql.Fields{
		"person": &graphql.Field{
			Type: personType,
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				return personSource, nil
			},
		},
	}
	rootQuery := graphql.ObjectConfig{Name: "RootQuery", Fields: fields}
	schemaConfig := graphql.SchemaConfig{Query: graphql.NewObject(rootQuery)}
	schema, err := graphql.NewSchema(schemaConfig)
	if err != nil {
		log.Fatalf("failed to create new schema, error: %v", err)
	}

	// Query
	query := `
		{
			person{
				name,
				dob,
				home{street,city},
				friends{name,address},
				age,
				weight,
				alive,
				hobbies
			}
		}
	`
	params := graphql.Params{Schema: schema, RequestString: query}
	r := graphql.Do(params)
	if len(r.Errors) > 0 {
		log.Fatalf("failed to execute graphql operation, errors: %+v", r.Errors)
	}

	rJSON, _ := json.Marshal(r)
	data := struct {
		Data struct {
			Person Person `json:"person"`
		} `json:"data"`
	}{}
	err = json.Unmarshal(rJSON, &data)
	if err != nil {
		log.Fatalf("failed to unmarshal. error: %v", err)
	}

	newPerson := data.Data.Person
	if !reflect.DeepEqual(newPerson, personSource) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(personSource, newPerson))
	}
}

func TestBindArg(t *testing.T) {
	var friendObj = graphql.NewObject(graphql.ObjectConfig{
		Name:   "friend",
		Fields: graphql.BindFields(Friend{}),
	})

	fields := graphql.Fields{
		"friend": &graphql.Field{
			Type: friendObj,
			//it can be added more than one since it's a slice
			Args: graphql.BindArg(Friend{}, "name"),
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				if name, ok := p.Args["name"].(string); ok {
					for _, friend := range friendSource {
						if friend.Name == name {
							return friend, nil
						}
					}
				}
				return nil, nil
			},
		},
	}
	rootQuery := graphql.ObjectConfig{Name: "RootQuery", Fields: fields}
	schemaConfig := graphql.SchemaConfig{Query: graphql.NewObject(rootQuery)}
	schema, err := graphql.NewSchema(schemaConfig)
	if err != nil {
		log.Fatalf("failed to create new schema, error: %v", err)
	}

	// Query
	query := `
		{
			friend(name:"Arief"){
				address
			}
		}
	`
	params := graphql.Params{Schema: schema, RequestString: query}
	r := graphql.Do(params)
	if len(r.Errors) > 0 {
		log.Fatalf("failed to execute graphql operation, errors: %+v", r.Errors)
	}

	rJSON, _ := json.Marshal(r)

	data := struct {
		Data struct {
			Friend Friend `json:"friend"`
		} `json:"data"`
	}{}
	err = json.Unmarshal(rJSON, &data)
	if err != nil {
		log.Fatalf("failed to unmarshal. error: %v", err)
	}

	expectedAddress := "palembang"
	newFriend := data.Data.Friend
	if newFriend.Address != expectedAddress {
		t.Fatalf("Unexpected result, expected address to be %s but got %s", expectedAddress, newFriend.Address)
	}
}

type Numbers struct {
	SmallInt  int16    `json:"smallInt"`
	Byte      uint8    `json:"byte"`
	Unsigned  uint16   `json:"unsigned"`
	SmallInts []int16  `json:"smallInts"`
	Bytes     []uint8  `json:"bytes"`
	Unsigneds []uint16 `json:"unsigneds"`
	Wide32    uint32   `json:"wide32"`
	Wide64    uint64   `json:"wide64"`
}

// int16, uint8 and uint16 fit in GraphQL's 32-bit Int, so they bind as Int.
// uint, uint32 and uint64 do not: bound as Int, a value above MaxInt32 would
// serialize as null, so they keep the String fallback that carries every value.
func TestBindFields_IntTypes(t *testing.T) {
	fields := graphql.BindFields(Numbers{})
	for name, want := range map[string]graphql.Output{
		"smallInt":  graphql.Int,
		"byte":      graphql.Int,
		"unsigned":  graphql.Int,
		"smallInts": graphql.NewList(graphql.Int),
		"bytes":     graphql.NewList(graphql.Int),
		"unsigneds": graphql.NewList(graphql.Int),
		"wide32":    graphql.String,
		"wide64":    graphql.String,
	} {
		if got := fields[name].Type; got.String() != want.String() {
			t.Errorf("%s: expected type %v, got %v", name, want, got)
		}
	}

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "RootQuery",
			Fields: graphql.Fields{
				"numbers": &graphql.Field{
					Type: graphql.NewObject(graphql.ObjectConfig{Name: "Numbers", Fields: fields}),
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return Numbers{
							SmallInt:  -3,
							Byte:      250,
							Unsigned:  65000,
							SmallInts: []int16{-1, 2},
							Bytes:     []uint8{1, 255},
							Unsigneds: []uint16{0, 65535},
							Wide32:    4000000000,
							Wide64:    18446744073709551615,
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("failed to create new schema, error: %v", err)
	}
	params := graphql.Params{
		Schema:        schema,
		RequestString: `{ numbers { smallInt byte unsigned smallInts bytes unsigneds wide32 wide64 } }`,
	}
	want := `{"data":{"numbers":{"smallInt":-3,"byte":250,"unsigned":65000,` +
		`"smallInts":[-1,2],"bytes":[1,255],"unsigneds":[0,65535],` +
		`"wide32":"4000000000","wide64":"18446744073709551615"}}}`

	doJSON, _ := json.Marshal(graphql.Do(params))
	for path, got := range map[string][]byte{
		"Do":       doJSON,
		"DoAppend": graphql.DoAppend(params, nil),
	} {
		var gotV, wantV interface{}
		if err := json.Unmarshal(got, &gotV); err != nil {
			t.Fatalf("%s: invalid JSON %s: %v", path, got, err)
		}
		_ = json.Unmarshal([]byte(want), &wantV)
		if !reflect.DeepEqual(gotV, wantV) {
			t.Errorf("%s: expected %s, got %s", path, want, got)
		}
	}
}
