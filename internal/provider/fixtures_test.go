package provider

import "fmt"

const (
	idA = "0194b7a0-0000-7000-8000-00000000000a"
	idB = "0194b7a0-0000-7000-8000-00000000000b"
)

func sourceJSON(name, managedBy string) string {
	mb := "null"
	if managedBy != "" {
		mb = `"` + managedBy + `"`
	}
	return fmt.Sprintf(`{"apiVersion":"ligo.dihedron.org/v1alpha1","kind":"Source","displayName":"Ledger","labels":{"team":"fin"},
		"identity":{"trustDomain":"prod.example.org","svidRegex":"spiffe://prod.example.org/ns/fin/sa/.*"},
		"metadata":{"id":"%s","name":"%s","jurisdiction":"dc/corp-prod","state":"ACTIVE","revision":3,"managedBy":%s,"createdAt":"2026-11-01T10:00:00Z","createdBy":"x","updatedAt":"2026-11-02T10:00:00Z","updatedBy":"x","pendingChangeRequestId":null},
		"status":{"bindings":2},"visibility":{"mode":"PUBLIC"}}`, idA, name, mb)
}

func targetJSON(name string) string {
	return fmt.Sprintf(`{"apiVersion":"ligo.dihedron.org/v1alpha1","kind":"Target","accessLevels":[{"name":"read","grants":{"b":1,"a":["x"]}}],
		"definition":{"zeta":1,"alpha":{"y":2,"x":1}},"labels":{},
		"metadata":{"id":"%s","name":"%s","jurisdiction":"dc/corp-prod/ledger","platform":"pg-ledger","platformJurisdiction":"dc/corp-prod","platformType":"postgres.v1","realm":"prod","state":"ACTIVE","revision":1,"managedBy":null,"createdAt":"2026-11-01T10:00:00Z","createdBy":"x","updatedAt":"2026-11-02T10:00:00Z","updatedBy":"x"},
		"status":{"bindings":1},"visibility":{"mode":"PUBLIC"}}`, idB, name)
}

func bindingJSON(name string) string {
	return fmt.Sprintf(`{"apiVersion":"ligo.dihedron.org/v1alpha1","kind":"Binding","source":"ledger-app","target":"ledger-db","accessLevel":"read",
		"parameters":{"role":"r","a":{"z":1,"b":2}},"expiresAt":"2027-01-01T00:00:00Z","auth":null,"active":null,"requested":{},"spec":null,
		"metadata":{"id":"%s","name":"%s","platform":"pg-ledger","platformJurisdiction":"dc/corp-prod","platformType":"postgres.v1","realm":"prod","revision":2,"managedBy":"spiffe://prod.example.org/ci/terraform","sourceJurisdiction":"dc/corp-prod","targetJurisdiction":"dc/corp-prod/ledger","createdAt":"2026-11-01T10:00:00Z","createdBy":"x","updatedAt":"2026-11-02T10:00:00Z","updatedBy":"x","wiring":{}},
		"status":{"phase":"ACTIVE","message":"","attempt":1,"currentStep":"","lastTransitionTime":"2026-11-02T10:00:00Z","operation":"CREATE","propagationPending":false,"workflowId":"w"}}`, idA, name)
}

func page(items string, next string) string {
	n := "null"
	if next != "" {
		n = `"` + next + `"`
	}
	return `{"items":[` + items + `],"nextPageToken":` + n + `}`
}
