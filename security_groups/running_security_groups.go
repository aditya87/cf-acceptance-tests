package security_groups_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	. "github.com/cloudfoundry/cf-acceptance-tests/cats_suite_helpers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"

	"github.com/cloudfoundry-incubator/cf-test-helpers/cf"
	"github.com/cloudfoundry-incubator/cf-test-helpers/helpers"
	"github.com/cloudfoundry-incubator/cf-test-helpers/workflowhelpers"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/app_helpers"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/assets"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/random_name"
)

type AppsResponse struct {
	Resources []struct {
		Metadata struct {
			Url string
		}
	}
}

type StatsResponse map[string]struct {
	Stats struct {
		Host string
		Port int
	}
}

type DoraCurlResponse struct {
	Stdout     string
	Stderr     string
	ReturnCode int `json:"return_code"`
}

func pushApp(appName, buildpack string) {
	Expect(cf.Cf("push",
		appName,
		"--no-start",
		"-b", buildpack,
		"-m", DEFAULT_MEMORY_LIMIT,
		"-p", assets.NewAssets().Dora,
		"-d", Config.GetAppsDomain()).Wait(Config.CfPushTimeoutDuration())).To(Exit(0))
	app_helpers.SetBackend(appName)
}

func getAppHostIpAndPort(appName string) (string, int) {
	var appsResponse AppsResponse
	cfResponse := cf.Cf("curl", fmt.Sprintf("/v2/apps?q=name:%s", appName)).Wait(Config.DefaultTimeoutDuration()).Out.Contents()
	json.Unmarshal(cfResponse, &appsResponse)
	serverAppUrl := appsResponse.Resources[0].Metadata.Url

	var statsResponse StatsResponse
	cfResponse = cf.Cf("curl", fmt.Sprintf("%s/stats", serverAppUrl)).Wait(Config.DefaultTimeoutDuration()).Out.Contents()
	json.Unmarshal(cfResponse, &statsResponse)

	return statsResponse["0"].Stats.Host, statsResponse["0"].Stats.Port
}

func testAppConnectivity(clientAppName string, privateHost string, privatePort int) DoraCurlResponse {
	var doraCurlResponse DoraCurlResponse
	curlResponse := helpers.CurlApp(Config, clientAppName, fmt.Sprintf("/curl/%s/%d", privateHost, privatePort))
	json.Unmarshal([]byte(curlResponse), &doraCurlResponse)
	return doraCurlResponse
}

func getAppContainerIpAndPort(appName string) (string, int) {
	curlResponse := helpers.CurlApp(Config, appName, "/myip")
	containerIp := strings.TrimSpace(curlResponse)

	curlResponse = helpers.CurlApp(Config, appName, "/env/VCAP_APPLICATION")
	var env map[string]interface{}
	err := json.Unmarshal([]byte(curlResponse), &env)
	Expect(err).NotTo(HaveOccurred())
	containerPort := int(env["port"].(float64))

	return containerIp, containerPort
}

func createSecurityGroup(privateHost string, privatePort int, containerIp string, containerPort int) string {
	rules := fmt.Sprintf(
		`[{"destination":"%s","ports":"%d","protocol":"tcp"},
			{"destination":"%s","ports":"%d","protocol":"tcp"}]`,
		privateHost, privatePort, containerIp, containerPort)

	file, _ := ioutil.TempFile(os.TempDir(), "CATS-sg-rules")
	defer os.Remove(file.Name())
	file.WriteString(rules)

	rulesPath := file.Name()
	securityGroupName := random_name.CATSRandomName("SG")

	workflowhelpers.AsUser(TestSetup.AdminUserContext(), Config.DefaultTimeoutDuration(), func() {
		Expect(cf.Cf("create-security-group", securityGroupName, rulesPath).Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
	})

	return securityGroupName
}

func bindSecurityGroup(securityGroupName, orgName, spaceName string) {
	By("Applying security group")
	workflowhelpers.AsUser(TestSetup.AdminUserContext(), Config.DefaultTimeoutDuration(), func() {
		Expect(cf.Cf("bind-security-group", securityGroupName, orgName, spaceName).Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
	})
}

func unbindSecurityGroup(securityGroupName, orgName, spaceName string) {
	By("Unapplying security group")
	workflowhelpers.AsUser(TestSetup.AdminUserContext(), Config.DefaultTimeoutDuration(), func() {
		Expect(cf.Cf("unbind-security-group", securityGroupName, orgName, spaceName).Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
	})
}

func deleteSecurityGroup(securityGroupName string) {
	workflowhelpers.AsUser(TestSetup.AdminUserContext(), Config.DefaultTimeoutDuration(), func() {
		Expect(cf.Cf("delete-security-group", securityGroupName, "-f").Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
	})
}

func createDummyBuildpack() string {
	buildpack := random_name.CATSRandomName("BPK")
	buildpackZip := assets.NewAssets().SecurityGroupBuildpack

	workflowhelpers.AsUser(TestSetup.AdminUserContext(), Config.DefaultTimeoutDuration(), func() {
		Expect(cf.Cf("create-buildpack", buildpack, buildpackZip, "999").Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
	})
	return buildpack
}

func deleteBuildpack(buildpack string) {
	workflowhelpers.AsUser(TestSetup.AdminUserContext(), Config.DefaultTimeoutDuration(), func() {
		Expect(cf.Cf("delete-buildpack", buildpack, "-f").Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
	})
}

func getStagingOutput(appName string) func() *Session {
	return func() *Session {
		appLogsSession := cf.Cf("logs", "--recent", appName)
		Expect(appLogsSession.Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
		return appLogsSession
	}
}

var _ = SecurityGroupsDescribe("Security Groups", func() {
	var serverAppName, privateHost string
	var privatePort int

	BeforeEach(func() {
		serverAppName = random_name.CATSRandomName("APP")
		pushApp(serverAppName, Config.GetRubyBuildpackName())
		Expect(cf.Cf("start", serverAppName).Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

		privateHost, privatePort = getAppHostIpAndPort(serverAppName)
	})

	Describe("Running security-groups", func() {
		var clientAppName, securityGroupName string

		BeforeEach(func() {
			clientAppName = random_name.CATSRandomName("APP")
			pushApp(clientAppName, Config.GetRubyBuildpackName())
			Expect(cf.Cf("start", clientAppName).Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			By("Asserting default running security group configuration")
			doraCurlResponse := testAppConnectivity(clientAppName, privateHost, privatePort)
			Expect(doraCurlResponse.ReturnCode).NotTo(Equal(0), "Expected running security groups not to allow internal communication between app containers. Configure your running security groups to not allow traffic on internal networks, or disable this test by setting 'include_security_groups' to 'false' in '"+os.Getenv("CONFIG")+"'.")
		})

		AfterEach(func() {
			app_helpers.AppReport(serverAppName, Config.DefaultTimeoutDuration())
			Expect(cf.Cf("delete", serverAppName, "-f", "-r").Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			app_helpers.AppReport(clientAppName, Config.DefaultTimeoutDuration())
			Expect(cf.Cf("delete", clientAppName, "-f", "-r").Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			deleteSecurityGroup(securityGroupName)
		})

		It("allows previously-blocked ip traffic after applying a security group, and re-blocks it when the group is removed", func() {
			containerIp, containerPort := getAppContainerIpAndPort(serverAppName)
			securityGroupName = createSecurityGroup(privateHost, privatePort, containerIp, containerPort)
			bindSecurityGroup(securityGroupName, TestSetup.RegularUserContext().Org, TestSetup.RegularUserContext().Space)

			Expect(cf.Cf("restart", clientAppName).Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			By("Testing that app can connect")
			doraCurlResponse := testAppConnectivity(clientAppName, privateHost, privatePort)
			Expect(doraCurlResponse.ReturnCode).To(Equal(0))

			unbindSecurityGroup(securityGroupName, TestSetup.RegularUserContext().Org, TestSetup.RegularUserContext().Space)
			Expect(cf.Cf("restart", clientAppName).Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			By("Testing that app can no longer connect")
			doraCurlResponse = testAppConnectivity(clientAppName, privateHost, privatePort)
			Expect(doraCurlResponse.ReturnCode).NotTo(Equal(0))
		})
	})

	Describe("staging security groups", func() {
		var testAppName, buildpack string

		BeforeEach(func() {
			By("Asserting default staging security group configuration")
			testAppName = random_name.CATSRandomName("APP")
			buildpack = createDummyBuildpack()
			pushApp(testAppName, buildpack)

			privateUri := fmt.Sprintf("%s:%d", privateHost, privatePort)
			Expect(cf.Cf("set-env", testAppName, "TESTURI", privateUri).Wait(Config.DefaultTimeoutDuration())).To(Exit(0))

			Expect(cf.Cf("start", testAppName).Wait(Config.CfPushTimeoutDuration())).To(Exit(1))
			Eventually(getStagingOutput(testAppName), 5).Should(Say("CURL_EXIT=[^0]"), "Expected staging security groups not to allow internal communication between app containers. Configure your staging security groups to not allow traffic on internal networks, or disable this test by setting 'include_security_groups' to 'false' in '"+os.Getenv("CONFIG")+"'.")
		})

		AfterEach(func() {
			app_helpers.AppReport(serverAppName, Config.DefaultTimeoutDuration())
			Expect(cf.Cf("delete", serverAppName, "-f", "-r").Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			app_helpers.AppReport(testAppName, Config.DefaultTimeoutDuration())
			Expect(cf.Cf("delete", testAppName, "-f", "-r").Wait(Config.CfPushTimeoutDuration())).To(Exit(0))

			deleteBuildpack(buildpack)
		})

		It("allows external and denies internal traffic during staging based on default staging security rules", func() {
			Expect(cf.Cf("set-env", testAppName, "TESTURI", "www.google.com").Wait(Config.DefaultTimeoutDuration())).To(Exit(0))
			Expect(cf.Cf("restart", testAppName).Wait(Config.CfPushTimeoutDuration())).To(Exit(1))
			Eventually(getStagingOutput(testAppName), 5).Should(Say("CURL_EXIT=0"))
		})
	})
})
