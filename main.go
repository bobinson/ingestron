package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/cayley/quad"
	"github.com/go-steem/rpc"
	"github.com/go-steem/rpc/transports/websocket"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/net"
	"github.com/tidwall/gjson"
	r "gopkg.in/dancannon/gorethink.v2"
)

const (
	numberGoroutines = 12
)

var wg sync.WaitGroup

func main() {

	Rsession, err := r.Connect(r.ConnectOpts{
		Address: string{"127.0.0.1:28015"},
	})
	if err != nil {
		log.Fatalln(err.Error())
	}

	// Create a table in the DB
	var rethinkdbname string = "steemit69"
	_, err = r.DBCreate(rethinkdbname).RunWrite(Rsession)
	Rsession.Use(rethinkdbname)
	if err != nil {
		fmt.Println("rethindb DB already made")
	}

	_, err = r.DB(rethinkdbname).TableCreate("operations").RunWrite(Rsession)
	if err != nil {
		fmt.Println("Probably already made a table for transactions")

	}
	//from here to the end of the function there's just one LOC that isn't about connecting to the chain.  should go in a library.
	// Process flags.
	flagAddress := flag.String("rpc_endpoint", "ws://127.0.0.1:8090", "steemd RPC endpoint address")
	flagReconnect := flag.Bool("reconnect", true, "enable auto-reconnect mode")
	flag.Parse()

	var (
		url       = *flagAddress
		reconnect = *flagReconnect
	)

	// Start catching signals.
	var interrupted bool
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	// Drop the error in case it is a request being interrupted.
	defer func() {
		if err == websocket.ErrClosing && interrupted {
			err = nil
		}
	}()
	// This allows you to tell the app which block to start on.
	// TODO: Make all of the vars into a config file and package the binaries
	// Start the connection monitor.
	monitorChan := make(chan interface{}, 1)
	if reconnect {
		go func() {
			for {
				event, ok := <-monitorChan
				if ok {
					log.Println(event)
				}
			}
		}()
	}

	// Instantiate the WebSocket transport.
	log.Printf("---> Dial(\"%v\")\n", url)
	t, err := websocket.NewTransport(url,
		websocket.SetAutoReconnectEnabled(reconnect),
		websocket.SetAutoReconnectMaxDelay(30*time.Second),
		websocket.SetMonitor(monitorChan))
	if err != nil {
		fmt.Println(err)
	}

	// Use the transport to get an RPC client.
	client, err := rpc.NewClient(t)
	if err != nil {
		fmt.Println(err)
	}
	defer func() {
		if !interrupted {
			client.Close()
		}
	}()

	// Start processing signals.
	go func() {
		<-signalCh
		fmt.Println()
		log.Println("Signal received, exiting...")
		signal.Stop(signalCh)
		interrupted = true
		client.Close()
	}()
	store, err := cayley.NewMemoryGraph()

	if err := run(client, Rsession, store); err != nil {
		log.Fatalln("Error:", err)
	}

}

// Run the application (opens channels, iterates through blockchains)
func run(client *rpc.Client, Rsession *r.Session, store *cayley.QuadStore) (err error) {

	// Keep processing incoming blocks forever.
	fmt.Println("---> Entering the block processing loop")
	for {
		// Get current properties.
		tasks := make(chan uint32, 1000000)
		donereading := make(chan string, 10000000)
		rethinknum := make(chan uint32, 10000000)
		rethinkwrite := make(chan string, 10000000)
		cayleynum := make(chan uint32, 10000000)
		cayleywrite := make(chan string, 10000000)
		blockreturn := make(chan string, 10000000)
		accountreturn := make(chan string, 10000000)

		if err != nil {
			return err
		}
		wg.Add(numberGoroutines)
		for gr := 1; gr <= numberGoroutines; gr++ {
			go Reader(tasks, gr, client)(opstrings)
			go Rethinkwrite(Rsession, rethinknum)
			go Cayleywrite(cayleynum, cayleywrite)
		}
		props, err := client.Database.GetDynamicGlobalProperties()

		for U := uint32(1); U <= uint32(props.LastIrreversibleBlockNum); U++ {
			tasks <- U
			rethinknums <- U
			cayleynums <- U
			opstructs := <-returnchannel
			cayleyops <- opstructs
			rethinkops <- opstructs

		}
		return err
	}
}

type reputations map[int]string
type votes map[int]string
type op map[int]string

type account struct {
	name          string      `json:"name"`
	created       string      `json:"created`
	mined         bool        `json:"mined"`
	post_count    int         `json:"post_count"`
	sbd_balance   string      `json:"sbd_balance"`
	witness_votes []string    `json:"witness_votes"`
	reputation    reputations `json:"reputation"`
	last_post     string      `json:"last_post"`
	voting_power  int         `json:"voting_power"`
}

type account_history struct {
	votes     `json:"result"`
	trxid     string         `json:"trx_id"`
	op        map[int]string `json:"op"`
	voter     string         `json:"voter"`
	author    string         `json:"author"`
	permlink  string         `json:"permlink"`
	weight    string         `json:"weight"`
	timestamp string         `json:"timestamp"`
}

func Reader(tasks chan uint32, gr int, client *rpc.Client, returnchannel chan string) {

	defer wg.Done()

	for {

		task := <-tasks

		fmt.Print("goroutine: ", gr, "     		block number: ", int(task), "Pulled from STEEM API\n")
		acctcount, err := client.Database.GetAccountCountRaw()
		block, err := client.Database.GetBlockRaw(task)                         //returns json.RawMessage
		blockstring := string(*block)                                           //this changes json.RawMessage into a string
		operations := gjson.Get(blockstring, "result.transactions#.operations") //now it is getting a string, because it doesn't accept json.rawmessage
		accounts := gjson.Get(blockstring, "result.transactions#.operations#.1.new_account_name")
		for _, account := range accounts {
			var accountstruct account
			var accountvotes account_votes
			json.Unmarshal(client.Database.GetAccountVotesRaw(account) & account_votes)
			json.Unmarshal(client.Database.GetAccountsRaw(account) & accountstruct)

		}
		strungagain := string(*operations) //strungagain gets rid of the pointer and makes the return from gjson a proper string
		returnchannel <- strungagain

	}
}

func Rethinkwrite(Rsession *r.Session) {
	defer wg.Done()
	for {
		cayleynum := <-cayleynums

		fmt.Print("goroutine: ", gr, "     		block number: ", int(task), "Written to Rethinkdb\n")
		r.Table("operations"). //rethinkdb inserts.
					Insert(operations).run(durability, "soft")
		Exec(Rsession)
	}
}

func Cayleywrite() {
	defer wg.Done()
	for {
		fmt.Print("goroutine: ", gr, "     		block number: ", int(task), "Written to Cayley In RAM\n")
		t := cayley.NewTransaction()
		t.AddQuad(quad.Make("food", "is", "good", nil))
		t.AddQuad(quad.Make("phrase of the day", "is of course", "Hello World!", nil))
		t.AddQuad(quad.Make("cats", "are", "awesome", nil))
		t.AddQuad(quad.Make("cats", "are", "scary", nil))
		t.AddQuad(quad.Make("cats", "want to", "kill you", nil))

	}

}

func monitoring() {
	for {
		time.Sleep(1000 * Millisecond)
		cpu, _ := cpu.InfoStat()
		netconnections, _ := net.ConnectionStat()
		mem, _ := mem.memory_info()
		disk, _ := disk.IOCounters()
	}

}
