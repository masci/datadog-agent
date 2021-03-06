package gui

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DataDog/datadog-agent/cmd/agent/common"
	"github.com/DataDog/datadog-agent/pkg/collector/check"
	"github.com/DataDog/datadog-agent/pkg/config"
	log "github.com/cihub/seelog"
	"github.com/gorilla/mux"
	yaml "gopkg.in/yaml.v2"
)

var (
	configPaths = []string{
		config.Datadog.GetString("confd_path"),        // Custom checks
		filepath.Join(common.GetDistPath(), "conf.d"), // Default check configs
	}

	checkPaths = []string{
		filepath.Join(common.GetDistPath(), "checks.d"), // Custom checks
		config.Datadog.GetString("additional_checksd"),  // Custom checks
		common.PyChecksPath,                             // Integrations-core checks
	}
)

// Adds the specific handlers for /checks/ endpoints
func checkHandler(r *mux.Router) {
	r.HandleFunc("/running", http.HandlerFunc(sendRunningChecks)).Methods("POST")
	r.HandleFunc("/run/{name}", http.HandlerFunc(runCheck)).Methods("POST")
	r.HandleFunc("/run/{name}/once", http.HandlerFunc(runCheckOnce)).Methods("POST")
	r.HandleFunc("/reload/{name}", http.HandlerFunc(reloadCheck)).Methods("POST")
	r.HandleFunc("/getConfig/{fileName}", http.HandlerFunc(getCheckConfigFile)).Methods("POST")
	r.HandleFunc("/getConfig/{checkFolder}/{fileName}", http.HandlerFunc(getCheckConfigFile)).Methods("POST")
	r.HandleFunc("/setConfig/{fileName}", http.HandlerFunc(setCheckConfigFile)).Methods("POST")
	r.HandleFunc("/setConfig/{checkFolder}/{fileName}", http.HandlerFunc(setCheckConfigFile)).Methods("POST")
	r.HandleFunc("/listChecks", http.HandlerFunc(listChecks)).Methods("POST")
	r.HandleFunc("/listConfigs", http.HandlerFunc(listConfigs)).Methods("POST")
}

// Sends a list of all the current running checks
func sendRunningChecks(w http.ResponseWriter, r *http.Request) {
	html, e := renderRunningChecks()
	if e != nil {
		w.Write([]byte("Error generating status html: " + e.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

// Schedules a specific check
func runCheck(w http.ResponseWriter, r *http.Request) {
	// Fetch the desired check
	name := mux.Vars(r)["name"]
	instances := common.AC.GetChecksByName(name)

	for _, ch := range instances {
		common.Coll.RunCheck(ch)
	}
	log.Infof("Scheduled new check: " + name)
	w.Write([]byte("Scheduled new check:" + name))
}

// Runs a specified check once
func runCheckOnce(w http.ResponseWriter, r *http.Request) {
	response := make(map[string]string)
	// Fetch the desired check
	name := mux.Vars(r)["name"]
	instances := common.AC.GetChecksByName(name)
	if len(instances) == 0 {
		html, e := renderError(name)
		if e != nil {
			html = "Error generating html: " + e.Error()
		}

		response["success"] = "" // empty string evaluates to false in JS
		response["html"] = html
		res, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		w.Write(res)
		return
	}

	// Run the check intance(s) once, as a test
	stats := []*check.Stats{}
	for _, ch := range instances {
		s := check.NewStats(ch)

		t0 := time.Now()
		err := ch.Run()
		warnings := ch.GetWarnings()
		mStats, _ := ch.GetMetricStats()
		s.Add(time.Since(t0), err, warnings, mStats)

		// Without a small delay some of the metrics will not show up
		time.Sleep(100 * time.Millisecond)

		stats = append(stats, s)
	}

	// Render the stats
	html, e := renderCheck(name, stats)
	if e != nil {
		response["success"] = ""
		response["html"] = "Error generating html: " + e.Error()
		res, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		w.Write(res)
		return
	}

	response["success"] = "true"
	response["html"] = html
	res, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

// Reloads a running check
func reloadCheck(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	instances := common.AC.GetChecksByName(name)
	if len(instances) == 0 {
		log.Errorf("Can't reload " + name + ": check has no new instances.")
		w.Write([]byte("Can't reload " + name + ": check has no new instances"))
		return
	}

	killed, e := common.Coll.ReloadAllCheckInstances(name, instances)
	if e != nil {
		log.Errorf("Error reloading check: " + e.Error())
		w.Write([]byte("Error reloading check: " + e.Error()))
		return
	}

	log.Infof("Removed %v old instance(s) and started %v new instance(s) of %s", len(killed), len(instances), name)
	w.Write([]byte(fmt.Sprintf("Removed %v old instance(s) and started %v new instance(s) of %s", len(killed), len(instances), name)))
}

// Sends the specified config (.yaml) file
func getCheckConfigFile(w http.ResponseWriter, r *http.Request) {
	fileName := mux.Vars(r)["fileName"]
	checkFolder := mux.Vars(r)["checkFolder"]
	if checkFolder != "" {
		fileName = filepath.Join(checkFolder, fileName)
	}

	var file []byte
	var e error
	for _, path := range configPaths {
		file, e = ioutil.ReadFile(filepath.Join(path, fileName))
		if e == nil {
			break
		}
	}
	if file == nil {
		w.Write([]byte("Error: Couldn't find " + fileName))
		return
	}

	w.Header().Set("Content-Type", "text")
	w.Write(file)
}

type configFormat struct {
	ADIdentifiers []string    `yaml:"ad_identifiers"`
	InitConfig    interface{} `yaml:"init_config"`
	MetricConfig  interface{} `yaml:"jmx_metrics"`
	Instances     []check.ConfigRawMap
}

// Overwrites a specific check's configuration (yaml) file with new data
// or makes a new config file for that check, if there isn't one yet
func setCheckConfigFile(w http.ResponseWriter, r *http.Request) {
	fileName := mux.Vars(r)["fileName"]
	checkFolder := mux.Vars(r)["checkFolder"]
	if checkFolder != "" {
		fileName = filepath.Join(checkFolder, fileName)
	}

	payload, e := parseBody(r)
	if e != nil {
		w.Write([]byte(e.Error()))
	}
	data := []byte(payload.Config)

	// Check that the data is actually a valid yaml file
	cf := configFormat{}
	e = yaml.Unmarshal(data, &cf)
	if e != nil {
		w.Write([]byte("Error: " + e.Error()))
		return
	}
	if len(cf.Instances) < 1 && cf.MetricConfig == nil {
		w.Write([]byte("Configuration file contains no valid instances"))
		return
	}

	// Attempt to write new configs to custom checks directory
	path := filepath.Join(config.Datadog.GetString("confd_path"), fileName)
	e = ioutil.WriteFile(path, data, 0600)

	// If the write didn't work, try writing to the default checks directory
	if e != nil && strings.Contains(e.Error(), "no such file or directory") {
		path = filepath.Join(common.GetDistPath(), "conf.d", fileName)
		e = ioutil.WriteFile(path, data, 0600)
	}

	if e != nil {
		w.Write([]byte("Error saving config file: " + e.Error()))
		log.Debug("Error saving config file: " + e.Error())
		return
	}

	log.Infof("Successfully wrote new " + fileName + " config file.")
	w.Write([]byte("Success"))
}

// Sends a list containing the names of all the checks
func listChecks(w http.ResponseWriter, r *http.Request) {
	filenames := []string{}
	for _, path := range checkPaths {
		files, err := ioutil.ReadDir(path)
		if err != nil {
			continue
		}

		for _, file := range files {
			if ext := filepath.Ext(file.Name()); ext == ".py" && file.Mode().IsRegular() {
				filenames = append(filenames, file.Name())
			}
		}
	}

	if len(filenames) == 0 {
		w.Write([]byte("No check (.py) files found."))
		return
	}

	res, _ := json.Marshal(filenames)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(res))
}

// Sends a list containing the names of all the config files
func listConfigs(w http.ResponseWriter, r *http.Request) {
	filenames := []string{}
	for _, path := range configPaths {
		files, e := readConfDir(path)

		if e == nil {
			// If a default config is found but a non-default version exists, ignore default
			sort.Strings(files)
			lookup := make(map[string]bool)
			for _, file := range files {
				checkName := file[:strings.Index(file, ".")]

				if ext := filepath.Ext(file); ext == ".default" {
					if _, exists := lookup[checkName]; exists {
						continue
					}
				}

				filenames = append(filenames, file)
				lookup[checkName] = true
			}
		}
	}

	if len(filenames) == 0 {
		w.Write([]byte("No configuration (.yaml) files found."))
		return
	}

	res, _ := json.Marshal(filenames)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(res))
}

// Helper function which returns all the filenames in a check config directory
func readConfDir(path string) ([]string, error) {
	var filenames []string
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return filenames, err
	}

	for _, entry := range entries {
		// Some check configs are in nested subdirectories
		if entry.IsDir() {
			if filepath.Ext(entry.Name()) != ".d" {
				continue
			}

			subEntries, err := ioutil.ReadDir(filepath.Join(path, entry.Name()))
			if err == nil {
				for _, subEntry := range subEntries {
					if hasRightEnding(subEntry.Name()) && subEntry.Mode().IsRegular() {
						// Save the full path of the config file {check_name.d}/{filename}
						filenames = append(filenames, entry.Name()+"/"+subEntry.Name())
					}
				}
			}
			continue
		}

		if hasRightEnding(entry.Name()) && entry.Mode().IsRegular() {
			filenames = append(filenames, entry.Name())
		}
	}

	return filenames, nil
}

// Helper function which checks if a file has a valid extension
func hasRightEnding(filename string) bool {
	// Only accept files of the format
	//	{name}.yaml, {name}.yml
	//	{name}.yaml.default, {name}.yml.default
	//	{name}.yaml.example, {name}.yml.example

	ext := filepath.Ext(filename)
	if ext == ".default" {
		ext = filepath.Ext(strings.TrimSuffix(filename, ".default"))
	} else if ext == ".example" {
		ext = filepath.Ext(strings.TrimSuffix(filename, ".example"))
	}

	return ext == ".yaml" || ext == ".yml"
}
