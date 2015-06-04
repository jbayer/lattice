package main_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/receptor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("LatticeCli Main", func() {
	const (
		latticeCliHomeVar = "LATTICE_CLI_HOME"
	)

	var (
		fakeServer *ghttp.Server
		homeFolder string
	)

	setupFakeServer := func() (target string) {
		fakeServer = ghttp.NewServer()

		serverAddress := strings.Split(fakeServer.Addr(), ":")
		Expect(serverAddress).To(HaveLen(2))
		portInt, err := strconv.Atoi(serverAddress[1])
		Expect(err).NotTo(HaveOccurred())

		fakeServer.AppendHandlers(ghttp.CombineHandlers(
			// ghttp.VerifyRequest("GET", "/v1/desired_lrps"),
			ghttp.RespondWithJSONEncoded(http.StatusOK, []receptor.DesiredLRPResponse{}, http.Header{
				"Content-Type": []string{"application/json"},
			}),
		))

		return fmt.Sprintf("%s.xip.io:%d", serverAddress[0], portInt)
	}

	targetFakeServer := func(target string) {
		var err error
		homeFolder, err = ioutil.TempDir("", "latticeHome")
		Expect(err).NotTo(HaveOccurred())

		command := exec.Command(ltcPath, "target", target)
		command.Env = []string{fmt.Sprintf("LATTICE_CLI_HOME=%s", homeFolder)}

		session, err := gexec.Start(command, GinkgoWriter, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred())
		Eventually(session).Should(gexec.Exit(0))
	}

	BeforeEach(func() {
		targetFakeServer(setupFakeServer())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(homeFolder)).To(Succeed())
		fakeServer.Close()
	})

	It("compiles and displays help text", func() {
		// targetFakeServer(setupFakeServer())
		command := exec.Command(ltcPath)
		command.Env = []string{fmt.Sprintf("LATTICE_CLI_HOME=%s", homeFolder)}

		session, err := gexec.Start(command, GinkgoWriter, GinkgoWriter)
		Expect(err).ToNot(HaveOccurred())
		Eventually(session).Should(gexec.Exit(0))
		Eventually(session.Out).Should(gbytes.Say("ltc - Command line interface for Lattice."))
		// fakeServer.Close()
		// Expect(fakeServer.ReceivedRequests()).To(BeEmpty())
	})

	Describe("Exit Codes", func() {

		// AfterEach(func() {
		// 	fakeServer.Close()
		// })

		It("exits non-zero when an unknown command is invoked", func() {
			command := exec.Command(ltcPath, "unknownCommand")
			command.Env = []string{fmt.Sprintf("LATTICE_CLI_HOME=%s", homeFolder)}

			session, err := gexec.Start(command, GinkgoWriter, GinkgoWriter)
			Expect(err).ToNot(HaveOccurred())
			Eventually(session, 3*time.Second).Should(gbytes.Say("not a registered command"))
			Eventually(session).Should(gexec.Exit(1))
			Expect(fakeServer.ReceivedRequests()).To(HaveLen(1))
		})

		It("exits non-zero when known command is invoked with invalid option", func() {
			command := exec.Command(ltcPath, "status", "--badFlag")
			command.Env = []string{fmt.Sprintf("LATTICE_CLI_HOME=%s", homeFolder)}

			session, err := gexec.Start(command, GinkgoWriter, GinkgoWriter)
			Expect(err).ToNot(HaveOccurred())
			Eventually(session).Should(gexec.Exit(1))
			// Expect(fakeServer.ReceivedRequests()).To(HaveLen(1))
		})
	})
})
