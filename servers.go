package steam

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"reflect"
	"sort"
	"sync"
	"time"

	"go/format"

	"github.com/manveru/go-steam/internal"
)

var lockCMServers = sync.RWMutex{}

func getRandomCM() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	lockCMServers.RLock()
	defer lockCMServers.RUnlock()
	return CMServers[rng.Int31n(int32(len(CMServers)))]
}

func updateServerList(pkg *internal.PacketMsg) {
	list := &internal.CMsgClientCMList{}
	pkg.ReadProtoMsg(list)

	lockCMServers.Lock()

	oldServers := CMServers
	CMServers = []string{}

	for idx, addr := range list.GetCmAddresses() {
		ip := ipv4FromInt(addr)
		port := list.GetCmPorts()[idx]
		CMServers = append(CMServers, fmt.Sprintf("%s:%d", ip, port))
	}

	sort.Strings(CMServers)

	lockCMServers.Unlock()

	if reflect.DeepEqual(oldServers, CMServers) {
		return
	}

	body := "package steam\nvar CMServers = []string{\n"

	lockCMServers.RLock()

	for _, server := range CMServers {
		body += "\"" + server + "\",\n"
	}

	lockCMServers.RUnlock()

	body += "}"

	formatted, err := format.Source([]byte(body))
	if err != nil {
		panic(err)
	}
	err = ioutil.WriteFile("server_list.totallynotgo", formatted, 0644)
	if err != nil {
		panic(err)
	}
}

func ipv4FromInt(rawIP uint32) net.IP {
	return net.IPv4(
		byte((rawIP>>24)&0xff),
		byte((rawIP>>16)&0xff),
		byte((rawIP>>8)&0xff),
		byte(rawIP&0xff),
	)
}
