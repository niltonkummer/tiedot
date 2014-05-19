// Database logic.
package dbsvc

import (
	"errors"
	"fmt"
	"github.com/HouzuoGuo/tiedot/tdlog"
	"net/rpc"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DBSvc struct {
	srvWorkingDir   string
	dataDir         string
	totalRank       int
	data            []*rpc.Client                  // Connections to data partitions
	schema          map[string]map[string][]string // Collection => Index name => Index path ^^
	mySchemaVersion int64
	lock            *sync.Mutex
}

// Create a new Client, connect to all server ranks.
func NewDBSvc(totalRank int, srvWorkingDir string, dataDir string) (db *DBSvc, err error) {
	db = &DBSvc{srvWorkingDir, dataDir, totalRank,
		make([]*rpc.Client, totalRank), make(map[string]map[string][]string), time.Now().UnixNano(),
		new(sync.Mutex)}
	for i := 0; i < totalRank; i++ {
		if db.data[i], err = rpc.Dial("unix", path.Join(srvWorkingDir, strconv.Itoa(i))); err != nil {
			return
		}
	}
	// Initialize data partitions
	var remoteSchemaVersion int64
	if err := db.data[0].Call("DataSvc.SchemaVersion", true, &remoteSchemaVersion); err != nil {
		tdlog.Panicf("Error during DB initialization: %v", err)
	}
	if remoteSchemaVersion == 0 {
		tdlog.Println("Intiialize database partitions")
		if err := db.loadSchema(true); err != nil {
			tdlog.Panicf("Error duing DB initialization: %v", err)
		}
	} else if err := db.loadSchema(false); err != nil {
		tdlog.Panicf("Error during DB initialization: %v", err)
	}
	return
}

// Sync & close all data partitions, then reopen everything.
func (db *DBSvc) Sync() error {
	db.lock.Lock()
	defer db.lock.Unlock()
	db.lockAllData()
	defer db.unlockAllData()
	db.unloadAll()
	return db.loadSchema(true)
}

// Shutdown all data partitions.
func (db *DBSvc) Shutdown() (err error) {
	db.lock.Lock()
	defer db.lock.Unlock()
	discard := new(bool)
	errs := make([]string, 0, 1)
	for i, srv := range db.data {
		if err := srv.Call("DataSvc.Shutdown", false, discard); err == nil || !strings.Contains(fmt.Sprint(err), "unexpected EOF") {
			errs = append(errs, fmt.Sprintf("Could not shutdown server rank %d", i))
		}
	}
	if len(errs) > 0 {
		err = errors.New(strings.Join(errs, "; "))
		tdlog.Errorf("Shutdown did not fully complete, but best effort has been made: %v", err)
	}
	return
}