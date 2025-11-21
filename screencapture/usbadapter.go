package screencapture

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/pkg/errors"

	"github.com/google/gousb"
	log "github.com/sirupsen/logrus"
)

// UsbAdapter reads and writes from AV Quicktime USB Bulk endpoints
type UsbAdapter struct {
	outEndpoint   *gousb.OutEndpoint
	Dump          bool
	DumpOutWriter io.Writer
	DumpInWriter  io.Writer
}

// WriteDataToUsb implements the UsbWriter interface and sends the byte array to the usb bulk endpoint.
func (usbAdapter *UsbAdapter) WriteDataToUsb(bytes []byte) {
	_, err := usbAdapter.outEndpoint.Write(bytes)
	if err != nil {
		log.Error("failed sending to usb", err)
	}
	if usbAdapter.Dump {
		_, err := usbAdapter.DumpOutWriter.Write(bytes)
		if err != nil {
			log.Fatalf("Failed dumping data:%v", err)
		}
	}
}

// StartReading claims the AV Quicktime USB Bulk endpoints and starts reading until a stopSignal is sent.
// Every received data is added to a frameextractor and when it is complete, sent to the UsbDataReceiver.
func (usbAdapter *UsbAdapter) StartReading(device IosDevice, receiver UsbDataReceiver, stopSignal chan interface{}) error {
	ctx, cleanUp, err := createContext()
	if err != nil {
		return err
	}
	defer cleanUp()

	usbDevice, err := OpenDevice(ctx, device)
	if err != nil {
		return err
	}
	// Detach any kernel drivers so we can claim the QuickTime interface ourselves.
	if err := usbDevice.SetAutoDetach(true); err != nil {
		log.Debugf("Auto-detach kernel drivers not enabled: %v", err)
	}
	if !device.IsActivated() {
		return errors.New("device not activated for screen mirroring")
	}

	confignum, _ := usbDevice.ActiveConfigNum()
	log.Debugf("Config is active: %d, QT config is: %d", confignum, device.QTConfigIndex)

	config, err := selectQuicktimeConfig(usbDevice, device.QTConfigIndex)
	if err != nil {
		return errors.Wrap(err, "Could not retrieve config")
	}

	log.Debugf("QT Config is active: %s", config.String())

	iface, err := findAndClaimQuickTimeInterface(config)
	if err != nil {
		log.Debug("could not get Quicktime Interface")
		return errors.Wrap(err, "claiming QuickTime USB interface")
	}
	log.Debugf("Got QT iface:%s", iface.String())

	inboundBulkEndpointIndex, inboundBulkEndpointAddress, err := findBulkEndpoint(iface.Setting, gousb.EndpointDirectionIn)
	if err != nil {
		return err
	}

	outboundBulkEndpointIndex, outboundBulkEndpointAddress, err := findBulkEndpoint(iface.Setting, gousb.EndpointDirectionOut)
	if err != nil {
		return err
	}

	err = clearFeature(usbDevice, inboundBulkEndpointAddress, outboundBulkEndpointAddress)
	if err != nil {
		return err
	}

	inEndpoint, err := iface.InEndpoint(inboundBulkEndpointIndex)
	if err != nil {
		log.Error("couldnt get InEndpoint")
		return err
	}
	log.Debugf("Inbound Bulk: %s", inEndpoint.String())

	outEndpoint, err := iface.OutEndpoint(outboundBulkEndpointIndex)
	if err != nil {
		log.Error("couldnt get OutEndpoint")
		return err
	}
	log.Debugf("Outbound Bulk: %s", outEndpoint.String())
	usbAdapter.outEndpoint = outEndpoint

	stream, err := inEndpoint.NewStream(4096, 5)
	if err != nil {
		log.Fatal("couldnt create stream")
		return err
	}
	log.Debug("Endpoint claimed")
	log.Infof("Device '%s' USB connection ready, waiting for ping..", device.SerialNumber)
	go func() {
		lengthBuffer := make([]byte, 4)
		for {
			n, err := io.ReadFull(stream, lengthBuffer)
			if err != nil {
				log.Errorf("Failed reading 4bytes length with err:%s only received: %d", err, n)
				return
			}
			//the 4 bytes header are included in the length, so we need to subtract them
			//here to know how long the payload will be
			length := binary.LittleEndian.Uint32(lengthBuffer) - 4
			dataBuffer := make([]byte, length)

			n, err = io.ReadFull(stream, dataBuffer)
			if err != nil {
				log.Errorf("Failed reading payload with err:%s only received: %d/%d bytes", err, n, length)
				var signal interface{}
				stopSignal <- signal
				return
			}
			if usbAdapter.Dump {
				_, err := usbAdapter.DumpInWriter.Write(dataBuffer)
				if err != nil {
					log.Fatalf("Failed dumping data:%v", err)
				}
			}
			receiver.ReceiveData(dataBuffer)
		}
	}()

	<-stopSignal
	receiver.CloseSession()
	log.Info("Closing usb stream")

	err = stream.Close()
	if err != nil {
		log.Error("Error closing stream", err)
	}
	log.Info("Closing usb interface")
	iface.Close()

	sendQTDisableConfigControlRequest(usbDevice)
	log.Debug("Resetting device config")
	_, err = usbDevice.Config(device.UsbMuxConfigIndex)
	if err != nil {
		log.Warn(err)
	}

	return nil
}

func clearFeature(usbDevice *gousb.Device, inboundBulkEndpointAddress gousb.EndpointAddress, outboundBulkEndpointAddress gousb.EndpointAddress) error {
	val, err := usbDevice.Control(0x02, 0x01, 0, uint16(inboundBulkEndpointAddress), make([]byte, 0))
	if err != nil {
		return errors.Wrap(err, "clear feature failed")
	}
	log.Debugf("Clear Feature RC: %d", val)

	val, err = usbDevice.Control(0x02, 0x01, 0, uint16(outboundBulkEndpointAddress), make([]byte, 0))
	log.Debugf("Clear Feature RC: %d", val)
	return errors.Wrap(err, "clear feature failed")
}

func findBulkEndpoint(setting gousb.InterfaceSetting, direction gousb.EndpointDirection) (int, gousb.EndpointAddress, error) {
	for _, v := range setting.Endpoints {
		if v.Direction == direction {
			return v.Number, v.Address, nil

		}
	}
	return 0, 0, errors.New("Inbound Bulkendpoint not found")
}

func findAndClaimQuickTimeInterface(config *gousb.Config) (*gousb.Interface, error) {
	log.Debug("Looking for quicktime interface..")
	if found, ifaceIndex, altIndex := findInterfaceAltForSubclass(config.Desc, QuicktimeSubclass); found {
		iface, err := config.Interface(ifaceIndex, altIndex)
		if err == nil {
			log.Debugf("Found Quicktimeinterface: %d alt:%d", ifaceIndex, altIndex)
			return iface, nil
		}
		log.Debugf("Quicktime subclass interface %d alt:%d unavailable: %v", ifaceIndex, altIndex, err)
	} else {
		log.Debug("Quicktime subclass interface not found, falling back to vendor bulk interfaces")
	}

	// Try all vendor bulk interfaces until one can be claimed.
	for _, cand := range findVendorBulkInterfaces(config.Desc) {
		iface, err := config.Interface(cand.iface, cand.alt)
		if err != nil {
			log.Debugf("Vendor bulk interface %d alt:%d unavailable: %v", cand.iface, cand.alt, err)
			continue
		}
		log.Debugf("Found Quicktimeinterface: %d alt:%d", cand.iface, cand.alt)
		return iface, nil
	}
	return nil, fmt.Errorf("did not find interface %v", config)
}

// selectQuicktimeConfig chooses a configuration that exposes a QuickTime/vendor bulk interface.
// It prefers the active config if it fits, then the preferred config, then any matching config.
func selectQuicktimeConfig(usbDevice *gousb.Device, preferredConfig int) (*gousb.Config, error) {
	var lastErr error

	tryConfig := func(cfgNum int, desc gousb.ConfigDesc, label string) (*gousb.Config, error) {
		if ok, _ := findInterfaceForSubclass(desc, QuicktimeSubclass); ok {
			cfg, cfgErr := configWithDetachFallback(usbDevice, cfgNum)
			if cfgErr == nil {
				log.Debugf("Using %s USB config %d for QuickTime endpoints", label, cfgNum)
				return cfg, nil
			}
			lastErr = cfgErr
		}
		if ok, _ := findVendorBulkInterface(desc); ok {
			cfg, cfgErr := configWithDetachFallback(usbDevice, cfgNum)
			if cfgErr == nil {
				log.Debugf("Using %s USB config %d (vendor bulk fallback) for QuickTime endpoints", label, cfgNum)
				return cfg, nil
			}
			lastErr = cfgErr
		}
		return nil, nil
	}

	// Prefer the preferred (QT) config first.
	if preferredDesc, ok := usbDevice.Desc.Configs[preferredConfig]; ok {
		if cfg, err := tryConfig(preferredConfig, preferredDesc, "preferred"); cfg != nil || err != nil {
			return cfg, err
		}
	}

	// Then try the currently active config.
	if activeConfigNum, err := usbDevice.ActiveConfigNum(); err == nil {
		if activeDesc, ok := usbDevice.Desc.Configs[activeConfigNum]; ok && activeConfigNum != preferredConfig {
			if cfg, err := tryConfig(activeConfigNum, activeDesc, "active"); cfg != nil || err != nil {
				return cfg, err
			}
		}
	}

	// Finally, try every config.
	for cfgNum, cfgDesc := range usbDevice.Desc.Configs {
		if cfgNum == preferredConfig {
			continue
		}
		if cfg, err := tryConfig(cfgNum, cfgDesc, "fallback"); cfg != nil || err != nil {
			return cfg, err
		}
	}

	if lastErr != nil {
		return nil, errors.Wrap(lastErr, "no config exposes QuickTime bulk endpoints")
	}
	return nil, errors.New("no config exposes QuickTime bulk endpoints")
}

// configWithDetachFallback tries to set the config. If macOS blocks kernel driver detachment,
// retry with autodetach disabled so we can proceed when detaching is not permitted.
func configWithDetachFallback(usbDevice *gousb.Device, cfgNum int) (*gousb.Config, error) {
	cfg, err := usbDevice.Config(cfgNum)
	if err == nil {
		return cfg, nil
	}
	if strings.Contains(err.Error(), "Can't detach kernel driver") || strings.Contains(err.Error(), "bad access") {
		log.Debugf("Config %d failed with auto-detach: %v; retrying without auto-detach", cfgNum, err)
		if derr := usbDevice.SetAutoDetach(false); derr != nil {
			log.Debugf("Disabling auto-detach failed: %v", derr)
		}
		return usbDevice.Config(cfgNum)
	}
	return nil, err
}

type ifaceAlt struct {
	iface int
	alt   int
}

// findVendorBulkInterfaces returns all interfaces that expose vendor class with bulk in/out endpoints.
func findVendorBulkInterfaces(confDesc gousb.ConfigDesc) []ifaceAlt {
	var result []ifaceAlt
	for _, iface := range confDesc.Interfaces {
		for _, alt := range iface.AltSettings {
			if alt.Class != gousb.ClassVendorSpec {
				continue
			}
			hasIn := false
			hasOut := false
			for _, ep := range alt.Endpoints {
				if ep.TransferType != gousb.TransferTypeBulk {
					continue
				}
				if ep.Direction == gousb.EndpointDirectionIn {
					hasIn = true
				}
				if ep.Direction == gousb.EndpointDirectionOut {
					hasOut = true
				}
			}
			if hasIn && hasOut {
				result = append(result, ifaceAlt{iface: iface.Number, alt: alt.Alternate})
			}
		}
	}
	return result
}

// findInterfaceAltForSubclass mimics findInterfaceForSubclass but also returns the alt setting number.
func findInterfaceAltForSubclass(confDesc gousb.ConfigDesc, subClass gousb.Class) (bool, int, int) {
	for _, iface := range confDesc.Interfaces {
		for _, alt := range iface.AltSettings {
			isVendorClass := alt.Class == gousb.ClassVendorSpec
			isCorrectSubClass := alt.SubClass == subClass
			log.Debugf("iface:%v altsettings:%d isvendor:%t isub:%t", iface, len(iface.AltSettings), isVendorClass, isCorrectSubClass)
			if isVendorClass && isCorrectSubClass {
				return true, iface.Number, alt.Alternate
			}
		}
	}
	return false, -1, -1
}
