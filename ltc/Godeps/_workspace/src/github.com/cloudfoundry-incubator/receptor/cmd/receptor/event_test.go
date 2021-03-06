package main_test

import (
	"encoding/json"
	"time"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/receptor/serialization"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/bbserrors"
	oldmodels "github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/tedsuo/ifrit/ginkgomon"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Event", func() {
	var eventSource receptor.EventSource
	var events chan receptor.Event
	var done chan struct{}
	var desiredLRP oldmodels.DesiredLRP

	JustBeforeEach(func() {
		receptorProcess = ginkgomon.Invoke(receptorRunner)

		var err error
		eventSource, err = client.SubscribeToEvents()
		Expect(err).NotTo(HaveOccurred())

		events = make(chan receptor.Event)
		done = make(chan struct{})

		go func() {
			defer close(done)
			for {
				event, err := eventSource.Next()
				if err != nil {
					close(events)
					return
				}
				events <- event
			}
		}()

		rawMessage := json.RawMessage([]byte(`{"port":8080,"hosts":["primer-route"]}`))
		primerLRP := oldmodels.DesiredLRP{
			ProcessGuid: "primer-guid",
			Domain:      "primer-domain",
			RootFS:      "primer:rootfs",
			Routes: map[string]*json.RawMessage{
				"router": &rawMessage,
			},
			Action: &oldmodels.RunAction{
				User: "me",
				Path: "true",
			},
		}

		err = legacyBBS.DesireLRP(logger, primerLRP)
		Expect(err).NotTo(HaveOccurred())

	PRIMING:
		for {
			select {
			case <-events:
				break PRIMING
			case <-time.After(50 * time.Millisecond):
				routeMsg := json.RawMessage([]byte(`{"port":8080,"hosts":["garbage-route"]}`))
				err = legacyBBS.UpdateDesiredLRP(logger, primerLRP.ProcessGuid, oldmodels.DesiredLRPUpdate{
					Routes: map[string]*json.RawMessage{
						"router": &routeMsg,
					},
				})
				Expect(err).NotTo(HaveOccurred())
			}
		}

		err = legacyBBS.RemoveDesiredLRPByProcessGuid(logger, primerLRP.ProcessGuid)
		Expect(err).NotTo(HaveOccurred())

		var event receptor.Event
		for {
			Eventually(events).Should(Receive(&event))
			if event.EventType() == receptor.EventTypeDesiredLRPRemoved {
				break
			}
		}
	})

	AfterEach(func() {
		ginkgomon.Kill(receptorProcess)
		err := eventSource.Close()
		Expect(err).NotTo(HaveOccurred())
		Eventually(done).Should(BeClosed())
	})

	Describe("Desired LRPs", func() {
		BeforeEach(func() {
			routeMessage := json.RawMessage([]byte(`[{"port":8080,"hostnames":["original-route"]}]`))
			routes := map[string]*json.RawMessage{"cf-router": &routeMessage}

			desiredLRP = oldmodels.DesiredLRP{
				ProcessGuid: "some-guid",
				Domain:      "some-domain",
				RootFS:      "some:rootfs",
				Routes:      routes,
				Action: &oldmodels.RunAction{
					User: "me",
					Path: "true",
				},
			}
		})

		It("receives events", func() {
			By("creating a DesiredLRP")
			err := legacyBBS.DesireLRP(logger, desiredLRP)
			Expect(err).NotTo(HaveOccurred())

			desiredLRP, err := legacyBBS.DesiredLRPByProcessGuid(logger, desiredLRP.ProcessGuid)
			Expect(err).NotTo(HaveOccurred())

			var event receptor.Event
			Eventually(events).Should(Receive(&event))

			desiredLRPCreatedEvent, ok := event.(receptor.DesiredLRPCreatedEvent)
			Expect(ok).To(BeTrue())

			Expect(desiredLRPCreatedEvent.DesiredLRPResponse).To(Equal(serialization.DesiredLRPToResponse(desiredLRP)))

			By("updating an existing DesiredLRP")
			routeMessage := json.RawMessage([]byte(`[{"port":8080,"hostnames":["new-route"]}]`))
			newRoutes := map[string]*json.RawMessage{
				"cf-router": &routeMessage,
			}
			err = legacyBBS.UpdateDesiredLRP(logger, desiredLRP.ProcessGuid, oldmodels.DesiredLRPUpdate{Routes: newRoutes})
			Expect(err).NotTo(HaveOccurred())

			Eventually(events).Should(Receive(&event))

			desiredLRPChangedEvent, ok := event.(receptor.DesiredLRPChangedEvent)
			Expect(ok).To(BeTrue())
			Expect(desiredLRPChangedEvent.After.Routes).To(Equal(receptor.RoutingInfo(newRoutes)))

			By("removing the DesiredLRP")
			err = legacyBBS.RemoveDesiredLRPByProcessGuid(logger, desiredLRP.ProcessGuid)
			Expect(err).NotTo(HaveOccurred())

			Eventually(events).Should(Receive(&event))

			desiredLRPRemovedEvent, ok := event.(receptor.DesiredLRPRemovedEvent)
			Expect(ok).To(BeTrue())
			Expect(desiredLRPRemovedEvent.DesiredLRPResponse.ProcessGuid).To(Equal(desiredLRP.ProcessGuid))
		})
	})

	Describe("Actual LRPs", func() {
		const (
			processGuid = "some-process-guid"
			domain      = "some-domain"
		)

		var (
			key            oldmodels.ActualLRPKey
			instanceKey    oldmodels.ActualLRPInstanceKey
			newInstanceKey oldmodels.ActualLRPInstanceKey
			netInfo        oldmodels.ActualLRPNetInfo
		)

		BeforeEach(func() {
			desiredLRP = oldmodels.DesiredLRP{
				ProcessGuid: processGuid,
				Domain:      domain,
				RootFS:      "some:rootfs",
				Instances:   1,
				Action: &oldmodels.RunAction{
					Path: "true",
					User: "me",
				},
			}

			key = oldmodels.NewActualLRPKey(processGuid, 0, domain)
			instanceKey = oldmodels.NewActualLRPInstanceKey("instance-guid", "cell-id")
			newInstanceKey = oldmodels.NewActualLRPInstanceKey("other-instance-guid", "other-cell-id")
			netInfo = oldmodels.NewActualLRPNetInfo("1.1.1.1", []oldmodels.PortMapping{})
		})

		It("receives events", func() {
			By("creating a ActualLRP")
			err := legacyBBS.DesireLRP(logger, desiredLRP)
			Expect(err).NotTo(HaveOccurred())

			actualLRPGroup, err := bbsClient.ActualLRPGroupByProcessGuidAndIndex(desiredLRP.ProcessGuid, 0)
			Expect(err).NotTo(HaveOccurred())
			actualLRP := *actualLRPGroup.GetInstance()

			var event receptor.Event
			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPCreatedEvent{}))

			actualLRPCreatedEvent := event.(receptor.ActualLRPCreatedEvent)
			Expect(actualLRPCreatedEvent.ActualLRPResponse).To(Equal(serialization.ActualLRPProtoToResponse(actualLRP, false)))

			By("updating the existing ActualLR")
			err = legacyBBS.ClaimActualLRP(logger, key, instanceKey)
			Expect(err).NotTo(HaveOccurred())

			before := actualLRP
			actualLRPGroup, err = bbsClient.ActualLRPGroupByProcessGuidAndIndex(desiredLRP.ProcessGuid, 0)
			Expect(err).NotTo(HaveOccurred())
			actualLRP = *actualLRPGroup.GetInstance()

			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPChangedEvent{}))

			actualLRPChangedEvent := event.(receptor.ActualLRPChangedEvent)
			Expect(actualLRPChangedEvent.Before).To(Equal(serialization.ActualLRPProtoToResponse(before, false)))
			Expect(actualLRPChangedEvent.After).To(Equal(serialization.ActualLRPProtoToResponse(actualLRP, false)))

			By("evacuating the ActualLRP")
			_, err = legacyBBS.EvacuateRunningActualLRP(logger, key, instanceKey, netInfo, 0)
			Expect(err).To(Equal(bbserrors.ErrServiceUnavailable))

			evacuatingLRPGroup, err := bbsClient.ActualLRPGroupByProcessGuidAndIndex(desiredLRP.ProcessGuid, 0)
			Expect(err).NotTo(HaveOccurred())
			evacuatingLRP := *evacuatingLRPGroup.GetEvacuating()

			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPCreatedEvent{}))

			// this is a necessary hack until we migrate other things to protobufs or pointer structs
			actualLRPCreatedEvent = event.(receptor.ActualLRPCreatedEvent)
			response := actualLRPCreatedEvent.ActualLRPResponse
			response.Ports = nil
			Expect(response).To(Equal(serialization.ActualLRPProtoToResponse(evacuatingLRP, true)))

			// discard instance -> UNCLAIMED
			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPChangedEvent{}))

			By("starting and then evacuating the ActualLRP on another cell")
			err = legacyBBS.StartActualLRP(logger, key, newInstanceKey, netInfo)
			Expect(err).NotTo(HaveOccurred())

			// discard instance -> RUNNING
			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPChangedEvent{}))

			evacuatingBefore := evacuatingLRP
			_, err = legacyBBS.EvacuateRunningActualLRP(logger, key, newInstanceKey, netInfo, 0)
			Expect(err).To(Equal(bbserrors.ErrServiceUnavailable))

			evacuatingLRPGroup, err = bbsClient.ActualLRPGroupByProcessGuidAndIndex(desiredLRP.ProcessGuid, 0)
			Expect(err).NotTo(HaveOccurred())
			evacuatingLRP = *evacuatingLRPGroup.GetEvacuating()

			Expect(err).NotTo(HaveOccurred())

			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPChangedEvent{}))

			actualLRPChangedEvent = event.(receptor.ActualLRPChangedEvent)
			response = actualLRPChangedEvent.Before
			response.Ports = nil
			Expect(response).To(Equal(serialization.ActualLRPProtoToResponse(evacuatingBefore, true)))

			response = actualLRPChangedEvent.After
			response.Ports = nil
			Expect(response).To(Equal(serialization.ActualLRPProtoToResponse(evacuatingLRP, true)))

			// discard instance -> UNCLAIMED
			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPChangedEvent{}))

			By("removing the instance ActualLRP")
			actualLRPGroup, err = bbsClient.ActualLRPGroupByProcessGuidAndIndex(desiredLRP.ProcessGuid, 0)
			Expect(err).NotTo(HaveOccurred())
			actualLRP = *actualLRPGroup.Instance

			err = legacyBBS.RemoveActualLRP(logger, key, oldmodels.ActualLRPInstanceKey{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPRemovedEvent{}))

			// this is a necessary hack until we migrate other things to protobufs or pointer structs
			actualLRPRemovedEvent := event.(receptor.ActualLRPRemovedEvent)
			response = actualLRPRemovedEvent.ActualLRPResponse
			response.Ports = nil
			Expect(response).To(Equal(serialization.ActualLRPProtoToResponse(actualLRP, false)))

			By("removing the evacuating ActualLRP")
			err = legacyBBS.RemoveEvacuatingActualLRP(logger, key, newInstanceKey)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() receptor.Event {
				Eventually(events).Should(Receive(&event))
				return event
			}).Should(BeAssignableToTypeOf(receptor.ActualLRPRemovedEvent{}))

			Expect(event).To(BeAssignableToTypeOf(receptor.ActualLRPRemovedEvent{}))

			// this is a necessary hack until we migrate other things to protobufs or pointer structs
			actualLRPRemovedEvent = event.(receptor.ActualLRPRemovedEvent)
			response = actualLRPRemovedEvent.ActualLRPResponse
			response.Ports = nil
			Expect(response).To(Equal(serialization.ActualLRPProtoToResponse(evacuatingLRP, true)))
		})
	})
})
