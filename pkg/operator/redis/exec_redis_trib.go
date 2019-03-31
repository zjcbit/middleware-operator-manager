package redis

import (
	"bytes"
		"fmt"
	"io"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/remotecommand"
	"github.com/golang/glog"
)

const debug = false

// ExecToPodThroughAPI uninterractively exec to the pod with the command specified.
// :param string command: list of the str which specify the command.
// :param string pod_name: Pod name
// :param string namespace: namespace of the Pod.
// :param io.Reader stdin: Standerd Input if necessary, otherwise `nil`
// :return: string: Output of the command. (STDOUT)
//          string: Errors. (STDERR)
//           error: If any error has occurred otherwise `nil`


func (rco *RedisClusterOperator) ExecToPodThroughAPI(command []string, containerName, podName, namespace string, stdin io.Reader) (string, string, error) {

	glog.V(3).Infof("exec To Pod Through API %v/%v -- %v", namespace, podName, command)

	req := rco.defaultClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		return "", "", fmt.Errorf("error adding to scheme: %v", err)
	}

	parameterCodec := runtime.NewParameterCodec(scheme)
	req.VersionedParams(&v1.PodExecOptions{
		Command:   command,
		Container: containerName,
		Stdin:     stdin != nil,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, parameterCodec)

	if debug {
		fmt.Println("Request URL:", req.URL().String())
	}

	exec, err := remotecommand.NewSPDYExecutor(rco.kubeConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("error while creating Executor: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("error in Stream: %v", err)
	}

	return stdout.String(), stderr.String(), nil
}
//
//func main() {
//	flag.Parse()
//	var namespace, containerName, podName string
//	var command []string
//	/*fmt.Print("Enter namespace: ")
//	fmt.Scanln(&namespace)
//	fmt.Print("Enter name of the pod: ")
//	fmt.Scanln(&podName)
//	fmt.Print("Enter name of the container [leave empty if there is only one container]: ")
//	fmt.Scanln(&containerName)
//	fmt.Print("Enter the commmand to execute: ")
//	fmt.Scanln(&command)*/
//	/*namespace = "kube-system"
//	podName = "redis-cluster-0"*/
//	//ok
//	//command = []string{"/bin/sh", "-c", `ls -l /`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb info 10.168.78.119:6379`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb del-node 10.168.78.119:6379 c49a3b06ad93638037d56855ff702787ad16e3ea`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb add-node 10.168.78.127:6379 10.168.78.119:6379`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb check 10.168.78.119:6379`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb reshard --from all --to c49a3b06ad93638037d56855ff702787ad16e3ea --slots 100 --yes 10.168.78.119:6379`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb reshard --from c49a3b06ad93638037d56855ff702787ad16e3ea --to 174ad1122349c33c475dcbd54489ea847ad8474f --slots 100 --yes 10.168.78.119:6379`}
//	//ok
//	//command = []string{"/bin/sh", "-c", `redis-trib.rb  rebalance --use-empty-masters 10.168.78.119:6379`}
//
//	//command = []string{"/bin/sh", "-c", `echo yes | redis-trib.rb create 10.168.78.67:6379 10.168.78.124:6379 10.168.78.126:6379`}
//
//
//	// For now I am assuming stdin for the command to be nil
//	output, stderr, err := ExecToPodThroughAPI(command, containerName, podName, namespace, nil)
//
//	if len(stderr) != 0 {
//		fmt.Println("STDERR:", stderr)
//	}
//	if err != nil {
//		fmt.Printf("Error occured while `exec`ing to the Pod %q, namespace %q, command %q. Error: %+v\n", podName, namespace, command, err)
//		fmt.Printf("Stderr:\n%v\nStdout:\n%v", stderr, output)
//	} else {
//		fmt.Println("Output:")
//		fmt.Println(output)
//	}
//}