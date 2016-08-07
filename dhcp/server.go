package dhcp // import "github.com/cafebazaar/blacksmith/dhcp"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/cafebazaar/blacksmith/datasource"
	"github.com/cafebazaar/blacksmith/logging"
	"github.com/krolaw/dhcp4"
)

const (
	minLeaseHours = 24
	maxLeaseHours = 48

	debugTag = "DHCP"
)

func randLeaseDuration() time.Duration {
	n := (minLeaseHours + rand.Intn(maxLeaseHours-minLeaseHours))
	return time.Duration(n) * time.Hour
}

// StartDHCP ListenAndServe for dhcp on port 67, binds on interface=ifName if it's
// not empty
func StartDHCP(ifName string, serverIP net.IP, datasource datasource.DataSource) error {
	handler := &Handler{
		ifName:      ifName,
		serverIP:    serverIP,
		datasource:  datasource,
		bootMessage: fmt.Sprintf("Blacksmith (%s)", datasource.SelfInfo().Version),
	}

	logging.Log("DHCP", "Listening on %s:67 (interface: %s)", serverIP.String(), ifName)
	var err error
	if ifName != "" {
		err = dhcp4.ListenAndServeIf(ifName, handler)
	} else {
		err = dhcp4.ListenAndServe(handler)
	}

	// https://groups.google.com/forum/#!topic/coreos-user/Qbn3OdVtrZU
	if len(datasource.ClusterName()) > 50 { // 63 - 12(mac) - 1(.)
		logging.Log(debugTag, "Warning: ClusterName is too long. It may break the behaviour of the DHCP clients")
	}

	rand.Seed(time.Now().UTC().UnixNano())

	return err
}

// Handler is passed to dhcp4 package to handle DHCP packets
type Handler struct {
	ifName      string
	serverIP    net.IP
	datasource  datasource.DataSource
	dhcpOptions dhcp4.Options
	bootMessage string
}

// dnsAddressesForDHCP returns instances. marshalled as specified in
// rfc2132 (option 6), without the length byte
func dnsAddressesForDHCP(instances *[]datasource.InstanceInfo) []byte {
	var res []byte

	for _, instanceInfo := range *instances {
		res = append(res, instanceInfo.IP.To4()...)
	}

	return res
}

func (h *Handler) fillPXE() []byte {
	// PXE vendor options
	var pxe bytes.Buffer
	var l byte
	// Discovery Control - disable broadcast and multicast boot server discovery
	pxe.Write([]byte{6, 1, 3})
	// PXE boot server
	pxe.Write([]byte{8, 7, 0x80, 0x00, 1})
	pxe.Write(h.serverIP.To4())
	// PXE boot menu - one entry, pointing to the above PXE boot server
	l = byte(3 + len(h.bootMessage))
	pxe.Write([]byte{9, l, 0x80, 0x00, 9})
	pxe.WriteString(h.bootMessage)
	// PXE menu prompt+timeout
	l = byte(1 + len(h.bootMessage))
	pxe.Write([]byte{10, l, 0x2})
	pxe.WriteString(h.bootMessage)
	// End vendor options
	pxe.WriteByte(255)
	return pxe.Bytes()
}

// ServeDHCP replies a dhcp request
func (h *Handler) ServeDHCP(p dhcp4.Packet, msgType dhcp4.MessageType, options dhcp4.Options) (d dhcp4.Packet) {

	switch msgType {
	case dhcp4.Discover, dhcp4.Request:
		if server, ok := options[dhcp4.OptionServerIdentifier]; ok && !net.IP(server).Equal(h.serverIP) {
			if msgType == dhcp4.Discover {
				logging.Debug("DHCP", "identifying dhcp server in Discover?! (%v)", p)
			}
			return nil // this message is not ours
		}

		machineInterface := h.datasource.MachineInterface(p.CHAddr())
		machine, err := machineInterface.Machine(true, nil)
		if err != nil {
			logging.Debug("DHCP", "failed to get machine for the mac (%s) %s",
				p.CHAddr().String(), err.Error())
			return nil
		}

		netConfStr, err := machineInterface.GetVariable(datasource.SpecialKeyNetworkConfiguration)
		if err != nil {
			logging.Log(debugTag, "failed to get network configuration: %s", err)
			return nil
		}

		var netConf networkConfiguration
		if err := json.Unmarshal([]byte(netConfStr), &netConf); err != nil {
			logging.Log(debugTag, "failed to unmarshal network configuration: %s / network configuration=%q",
				err, netConfStr)
			return nil
		}

		instanceInfos, err := h.datasource.Instances()
		if err != nil {
			logging.Log(debugTag, "failed to get instances: %s", err)
			return nil
		}

		hostname := strings.Join(strings.Split(p.CHAddr().String(), ":"), "")
		hostname += "." + h.datasource.ClusterName()

		dhcpOptions := dhcp4.Options{
			dhcp4.OptionSubnetMask:       netConf.Netmask.To4(),
			dhcp4.OptionDomainNameServer: dnsAddressesForDHCP(&instanceInfos),
			dhcp4.OptionHostName:         []byte(hostname),
		}

		if netConf.Router != nil {
			dhcpOptions[dhcp4.OptionRouter] = netConf.Router.To4()
		}
		if len(netConf.ClasslessRouteOption) != 0 {
			var res []byte
			for _, part := range netConf.ClasslessRouteOption {
				res = append(res, part.toBytes()...)
			}
			dhcpOptions[dhcp4.OptionClasslessRouteFormat] = res

		}

		responseMsgType := dhcp4.Offer
		if msgType == dhcp4.Request {
			responseMsgType = dhcp4.ACK

			requestedIP := net.IP(options[dhcp4.OptionRequestedIPAddress])
			if requestedIP == nil {
				requestedIP = net.IP(p.CIAddr())
			}
			if len(requestedIP) != 4 || requestedIP.Equal(net.IPv4zero) {
				logging.Debug("DHCP", "dhcp %s - CHADDR %s - bad request",
					msgType, p.CHAddr().String())
				return nil
			}
			if !requestedIP.Equal(machine.IP) {
				logging.Log("DHCP", "dhcp %s - CHADDR %s - requestedIP(%s) != assignedIp(%s)",
					msgType, p.CHAddr().String(), requestedIP.String(), machine.IP.String())
				return nil
			}

			machineInterface.CheckIn()
		}

		guidVal, isPxe := options[97]

		logging.Debug("DHCP", "dhcp %s - CHADDR %s - assignedIp %s - isPxe %v",
			msgType, p.CHAddr().String(), machine.IP.String(), isPxe)

		replyOptions := dhcpOptions.SelectOrderOrAll(options[dhcp4.OptionParameterRequestList])

		if isPxe { // this is a pxe request
			guid := guidVal[1:]
			replyOptions = append(replyOptions,
				dhcp4.Option{
					Code:  dhcp4.OptionVendorClassIdentifier,
					Value: []byte("PXEClient"),
				},
				dhcp4.Option{
					Code:  97, // UUID/GUID-based Client Identifier
					Value: guid,
				},
				dhcp4.Option{
					Code:  dhcp4.OptionVendorSpecificInformation,
					Value: h.fillPXE(),
				},
			)
		}
		packet := dhcp4.ReplyPacket(p, responseMsgType, h.serverIP, machine.IP,
			randLeaseDuration(), replyOptions)
		return packet

	case dhcp4.Release, dhcp4.Decline:
		return nil
	}
	return nil
}
