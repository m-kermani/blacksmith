package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"

	"github.com/cafebazaar/blacksmith/datasource"
)

// Version returns json encoded version details
func (ws *webServer) Version(w http.ResponseWriter, r *http.Request) {
	versionJSON, err := json.Marshal(ws.ds.SelfInfo())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), 500)
		return
	}
	io.WriteString(w, string(versionJSON))
}

type machineDetails struct {
	Name          string                 `json:"name"`
	Nic           string                 `json:"nic"`
	IP            net.IP                 `json:"ip"`
	Type          datasource.MachineType `json:"type"`
	FirstAssigned int64                  `json:"firstAssigned"`
	LastAssigned  int64                  `json:"lastAssigned"`
}

func machineToDetails(machineInterface datasource.MachineInterface) (*machineDetails, error) {

	name := machineInterface.Hostname()
	mac := machineInterface.Mac()

	machine, err := machineInterface.Machine(true, nil)

	if err != nil {
		return nil, errors.New("stats")
	}
	last, err := machineInterface.LastSeen()
	if err != nil {
		return nil, errors.New("LAST")
	}
	return &machineDetails{
		name, mac.String(),
		machine.IP, machine.Type,
		machine.FirstSeen, last}, nil
}

// MachinesList creates a list of the currently known machines based on the etcd
// entries
func (ws *webServer) MachinesList(w http.ResponseWriter, r *http.Request) {
	machines, err := ws.ds.MachineInterfaces()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}
	if len(machines) == 0 {
		io.WriteString(w, "[]")
		return
	}
	machinesArray := make([]*machineDetails, 0, len(machines))
	for _, machine := range machines {
		l, err := machineToDetails(machine)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
			return
		}
		if l != nil {
			machinesArray = append(machinesArray, l)
		}
	}

	machinesJSON, err := json.Marshal(machines)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}
	io.WriteString(w, string(machinesJSON))
}

// ClusterVariables returns all the cluster general variables
func (ws *webServer) ClusterVariablesList(w http.ResponseWriter, r *http.Request) {
	flags, err := ws.ds.ListClusterVariables()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}

	flagsJSON, err := json.Marshal(flags)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}
	io.WriteString(w, string(flagsJSON))
}

// MachineVariable returns all the flags set for the machine
func (ws *webServer) MachineVariables(w http.ResponseWriter, r *http.Request) {
	_, macStr := path.Split(r.URL.Path)

	mac, err := net.ParseMAC(macStr)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}

	machineInterface := ws.ds.MachineInterface(mac)

	flags, err := machineInterface.ListVariables()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}

	flagsJSON, err := json.Marshal(flags)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err), http.StatusInternalServerError)
		return
	}
	io.WriteString(w, string(flagsJSON))
}

func (ws *webServer) SetMachineVariable(w http.ResponseWriter, r *http.Request) {
	_, name := path.Split(r.URL.Path)
	value := r.FormValue("value")

	macStr := r.FormValue("mac")
	var machineInterface datasource.MachineInterface
	if macStr != "" {
		mac, err := net.ParseMAC(macStr)
		if err != nil {
			http.Error(w, `{"error": "Error while parsing the mac"}`, http.StatusInternalServerError)
			return
		}

		machineInterface = ws.ds.MachineInterface(mac)

	}

	var err error

	err = machineInterface.SetVariable(name, value)

	if err != nil {
		http.Error(w, `{"error": "Error while setting value"}`, http.StatusInternalServerError)
		return
	}

	io.WriteString(w, `"OK"`)
}

func (ws *webServer) DelMachineVariable(w http.ResponseWriter, r *http.Request) {
	_, name := path.Split(r.URL.Path)

	macStr := r.FormValue("mac")
	var machineInterface datasource.MachineInterface
	if macStr != "" {
		mac, err := net.ParseMAC(macStr)
		if err != nil {
			http.Error(w, `{"error": "Error while parsing the mac"}`, http.StatusInternalServerError)
			return
		}

		machineInterface = ws.ds.MachineInterface(mac)
	}

	var err error
	machineInterface.DeleteVariable(name)
	if err != nil {
		http.Error(w, `{"error": "Error while delleting value"}`, http.StatusInternalServerError)
		return
	}

	io.WriteString(w, `"OK"`)
}

func (ws *webServer) SetVariable(w http.ResponseWriter, r *http.Request) {
	_, name := path.Split(r.URL.Path)
	value := r.FormValue("value")

	var err error
	err = ws.ds.SetClusterVariable(name, value)

	if err != nil {
		http.Error(w, `{"error": "Error while setting value"}`, http.StatusInternalServerError)
		return
	}

	io.WriteString(w, `"OK"`)
}

func (ws *webServer) DelVariable(w http.ResponseWriter, r *http.Request) {
	_, name := path.Split(r.URL.Path)

	err := ws.ds.DeleteClusterVariable(name)

	if err != nil {
		http.Error(w, `{"error": "Error while delleting value"}`, http.StatusInternalServerError)
		return
	}

	io.WriteString(w, `"OK"`)
}
