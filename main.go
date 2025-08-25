package main

import (
    "flag"
    "os"

    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"

    "github.com/rkitindi-kr/pvc-webhook/controllers" // adjust module path
)

func main() {
    var metricsAddr string
    var enableLeaderElection bool

    flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
    flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
    flag.Parse()

    opts := zap.Options{}
    logger := zap.New(zap.UseFlagOptions(&opts))
    ctrl.SetLogger(logger)

    mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
        MetricsBindAddress: metricsAddr,
        LeaderElection:     enableLeaderElection,
        LeaderElectionID:   "pvc-webhook-controller",
    })
    if err != nil {
        os.Exit(1)
    }

    if err = (&controllers.PersistentVolumeClaimReconciler{
        Client: mgr.GetClient(),
        Scheme: mgr.GetScheme(),
    }).SetupWithManager(mgr); err != nil {
        os.Exit(1)
    }

    if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
        os.Exit(1)
    }
}

