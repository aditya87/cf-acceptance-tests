package routing

import (
	"fmt"
	"time"

	. "github.com/cloudfoundry/cf-acceptance-tests/cats_suite_helpers"

	. "code.cloudfoundry.org/cf-routing-test-helpers/helpers"
	"github.com/cloudfoundry-incubator/cf-test-helpers/helpers"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/assets"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/random_name"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/skip_messages"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = RoutingDescribe("Multiple App Ports", func() {
	var (
		app             string
		secondRoute     string
		latticeAppAsset = assets.NewAssets().LatticeApp
		SleepTimeOut    = 45 * time.Second
	)

	BeforeEach(func() {
		if Config.Backend != "diego" {
			Skip(skip_messages.SkipDiegoMessage)
		}
		app = random_name.CATSRandomName("APP")
		cmd := fmt.Sprintf("lattice-app --ports=7777,8888,8080")

		PushAppNoStart(app, latticeAppAsset, Config.GoBuildpackName, Config.AppsDomain, Config.CfPushTimeoutDuration(), DEFAULT_MEMORY_LIMIT, "-c", cmd)
		EnableDiego(app, Config.DefaultTimeoutDuration())
		StartApp(app, APP_START_TIMEOUT)
		if Config.SleepTimeoutDuration() > 0 {
			SleepTimeOut = time.Duration(Config.SleepTimeout) * time.Second
		}
	})

	AfterEach(func() {
		AppReport(app, Config.DefaultTimeoutDuration())
		DeleteApp(app, Config.DefaultTimeoutDuration())
	})

	Context("when app only has single route", func() {
		Context("when no ports are specified for the app", func() {
			It("should listen on the default app port", func() {
				Eventually(func() string {
					return helpers.CurlApp(app, "/port")
				}, Config.DefaultTimeoutDuration(), "5s").Should(ContainSubstring("8080"))
			})
		})
	})

	Context("when app has multiple ports mapped", func() {
		BeforeEach(func() {
			UpdatePorts(app, []uint16{7777, 8888, 8080}, Config.DefaultTimeoutDuration())
			// create 2nd route
			spacename := TestSetup.RegularUserContext().Space
			secondRoute = fmt.Sprintf("%s-two", app)
			CreateRoute(secondRoute, "", spacename, Config.AppsDomain, Config.DefaultTimeoutDuration())

			// map app route to other port
			CreateRouteMapping(app, secondRoute, 0, 7777, Config.DefaultTimeoutDuration())
		})

		It("should listen on multiple ports", func() {
			Eventually(func() string {
				return helpers.CurlApp(app, "/")
			}, Config.DefaultTimeoutDuration(), "5s").Should(ContainSubstring("Lattice"))

			Consistently(func() string {
				return helpers.CurlApp(app, "/port")
			}, SleepTimeOut, "5s").Should(ContainSubstring("8080"))

			Eventually(func() string {
				return helpers.CurlApp(secondRoute, "/port")
			}, Config.DefaultTimeoutDuration(), "5s").Should(ContainSubstring("7777"))
		})
	})
})
