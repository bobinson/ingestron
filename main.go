package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/quad"
	"github.com/faddat/steemconnect"
	"github.com/go-steem/rpc"
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
		Address: "127.0.0.1:28015",
	})
	if err != nil {
		log.Fatalln(err.Error())
	}

	// Create a table in the DB
	var rethinkdbname = "steemit69"
	_, err = r.DBCreate(rethinkdbname).RunWrite(Rsession)
	Rsession.Use(rethinkdbname)
	if err != nil {
		fmt.Println("rethindb DB already made")
	}

	_, err = r.DB(rethinkdbname).TableCreate("operations").RunWrite(Rsession)
	if err != nil {
		fmt.Println("Probably already made a table for transactions")
	}

	_, err = r.DB(rethinkdbname).TableCreate("accounts").RunWrite(Rsession)
	if err != nil {
		fmt.Println("Probably already made a table for transactions")
	}

	client := steemconnect.Steemconnect()
	store, err := cayley.NewMemoryGraph()

	if err := run(client, Rsession, store); err != nil {
		log.Fatalln("Error:", err)
	}

}

// Run the application (opens channels, iterates through blockchains)
func run(client *rpc.Client, Rsession *r.Session, store cayley.QuadStore) (err error) {

	// Keep processing incoming blocks forever.
	fmt.Println("---> Entering the block processing loop")
	for {
		// Get current properties.

		tasks := make(chan uint32, 100000)
		donereading := make(chan string, 1000000)
		nums := make(chan uint32, 1000000)
		writes := make(chan string, 1000000)
		blockchan := make(chan string, 1000000)
		accountchan := make(chan account, 1000000)
		accounthistorychan := make(chan accountHistory, 1000000)
		votechan := make(chan voteHistory, 1000000)
		newaccount := make(chan string, 1000000)

		if err != nil {
			return err
		}

		wg.Add(numberGoroutines)
		for gr := 1; gr <= numberGoroutines; gr++ {
			go Reader(tasks, newaccount, accountchan, accounthistorychan, votechan, blockchan, gr, client)
			go Blockwrite(Rsession, store, nums, writes)
			go Votewrite(Rsession, Accountchan, Votechan)
			go Accountwrite(Rsession, votechan)
		}
		props, err := client.Database.GetDynamicGlobalProperties()

		for U := uint32(1); U <= uint32(props.LastIrreversibleBlockNum); U++ {
			tasks <- U
			rethinknums <- U
			cayleynums <- U
		}
		return err
	}
}

type account struct {
	Name         string         `json:"name"`
	Created      string         `json:"created"`
	Mined        bool           `json:"mined"`
	PostCount    int            `json:"post_count"`
	SbdBalance   string         `json:"sbd_balance"`
	WitnessVotes []string       `json:"witness_votes"`
	Reputation   map[int]string `json:"reputation"`
	LastPost     string         `json:"last_post"`
	VotingPower  int            `json:"voting_power"`
}

type accountHistory struct {
	Trxid     string         `json:"trx_id"`
	Op        map[int]string `json:"op"`
	Voter     string         `json:"voter"`
	Author    string         `json:"author"`
	Permlink  string         `json:"permlink"`
	Weight    string         `json:"weight"`
	Timestamp string         `json:"timestamp"`
}

type voteHistory struct {
	ID     int `json:"id"`
	Result []struct {
		Authorperm string `json:"authorperm"`
		Weight     int    `json:"weight"`
		Rshares    string `json:"rshares"`
		Percent    int    `json:"percent"`
		Time       string `json:"time"`
	} `json:"result"`
}

//Reader is responsible for gathering data
func Reader(tasks chan uint32, newaccount chan string, accountchan chan account, accounthistorychan chan accountHistory, votechan chan voteHistory, blockchan chan string, gr int, client *rpc.Client) {

	defer wg.Done()

	for {

		task := <-tasks
		fmt.Print("goroutine: ", gr, "     		block number: ", int(task), "Pulled from STEEM API\n")
		block, err := client.Database.GetBlockRaw(task)                         //returns json.RawMessage
		blockstring := string(*block)                                           //this changes json.RawMessage into a string
		operations := gjson.Get(blockstring, "result.transactions#.operations") //now it is getting a string, because it doesn't accept json.rawmessage
		accounts := gjson.Get(blockstring, "result.transactions#.operations#.#.new_account_name")
		accountnum := gjson.Get(blockstring, "result.transactions#.operations#.#.new_account_name.#")
		accountnumint := accountnum.Int()
		if accountnumint != 0 {
			fmt.Print("goroutine: ", gr, "no new accounts in block number: ", int(task), "Written to Rethinkdb\n")
			for _, newaccountname := range accounts.Array() {
				var err error
				var accountstruct account
				var acctvotes voteHistory
				var accountHistoryStruct accountHistory
				var accountslice []string
				accountinquestion := newaccountname.String()
				accounthistoryraw, err := client.Database.GetAccountHistoryRaw(accountinquestion, uint64(2000), uint32(1999))
				votesraw, err := client.Database.GetAccountVotesRaw(accountinquestion)
				accountslice[1] = accountinquestion
				accountraw, err := client.Database.GetAccountsRaw(accountslice)
				json.Unmarshal(*accounthistoryraw, accountHistoryStruct)
				json.Unmarshal(*votesraw, acctvotes)
				json.Unmarshal(*accountraw, accountstruct)
				newaccount <- accountinquestion
				votechan <- acctvotes
				accountchan <- accountstruct
			}
		}
		nums <- task
		writes <- operations.String()
	}
}

//Blockwrite writes block data to rethinkdb and cayley.
func Blockwrite(Rsession *r.Session, store cayley.QuadStore, nums chan uint32, writes chan string) {
	defer wg.Done()
	for {
		num := <-nums
		write := <-writes

		fmt.Print("goroutine: ", gr, "     		block number: ", int(task), "Written to Rethinkdb\n")
		r.Table("operations"). //rethinkdb inserts.
					Insert(operations).run(durability, "soft")
		Exec(Rsession)
		fmt.Print("goroutine: ", gr, "     		block number: ", int(task), "Written to Cayley In RAM\n")
		t := cayley.NewTransaction()
		t.AddQuad(quad.Make("food", "is", "good", nil))
	}
}

//Accountwrite writes accounts and votes.  Changed from cayleywrite because some blocks have no new accounts and some blocks have several.
func Accountwrite(Rsession *r.Session, newaccount chan string) {

	defer wg.Done()
	for {
		account := <-newaccount
		r.Table("accounts"). //rethinkdb inserts.
					Insert(account).run(durability, "soft")
		Exec(Rsession)

	}

}

//Votewrite writes a single user's votes to a rethinkdb table.
func Votewrite(Rsession *r.Session, votechan chan voteHistory, newaccount chan string) {

	defer wg.Done()
	for {
		vote := <-votechan
		account := <-newaccount
		_, err = r.DB(rethinkdbname).TableCreate(account).RunWrite(Rsession)
		r.Table(account).
			Insert(vote).run(durability, "soft")

	}

}

func monitoring() {
	defer wg.Done()
	for {
		time.Sleep(1000 * Millisecond)
		cpu, _ := cpu.InfoStat()
		netconnections, _ := net.ConnectionStat()
		Mem, _ := mem.memoryInfo()
		disk, _ := disk.IOCounters()
	}

}
