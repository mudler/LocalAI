package main

// Spawns the built timestral-cpp-grpc binary and calls it over gRPC with the
// 5M F32 model. This covers what the in-process specs cannot: the server
// registration, the model identity check, the lock around the RPC and the
// mapping of a bad request to InvalidArgument. It skips unless the binary, the
// library and TIMESTRAL_BACKEND_TEST_MODEL are all present.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var _ = Describe("timestral-cpp gRPC server", func() {
	It("loads a model and answers Forecast", func() {
		modelPath := os.Getenv("TIMESTRAL_BACKEND_TEST_MODEL")
		if modelPath == "" {
			Skip("TIMESTRAL_BACKEND_TEST_MODEL is not set")
		}
		binary, err := filepath.Abs("./timestral-cpp-grpc")
		Expect(err).ToNot(HaveOccurred())
		if _, err := os.Stat(binary); err != nil {
			Skip("backend binary not built: run `make timestral-cpp-grpc` first")
		}
		lib := os.Getenv("TIMESTRAL_LIBRARY")
		if lib == "" {
			lib, _ = filepath.Abs("./libtimestral.so")
		}
		if _, err := os.Stat(lib); err != nil {
			Skip("libtimestral.so not found: " + lib)
		}

		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr := l.Addr().String()
		Expect(l.Close()).To(Succeed())

		cmd := exec.Command(binary, "--addr", addr)
		cmd.Env = append(os.Environ(), "TIMESTRAL_LIBRARY="+lib)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		Expect(cmd.Start()).To(Succeed())
		defer func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}()

		deadline := time.Now().Add(10 * time.Second)
		for {
			c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				_ = c.Close()
				break
			}
			if time.Now().After(deadline) {
				Fail(fmt.Sprintf("backend did not listen on %s within 10s", addr))
			}
			time.Sleep(100 * time.Millisecond)
		}

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = conn.Close() }()
		client := pb.NewBackendClient(conn)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		loaded, err := client.LoadModel(ctx, &pb.ModelOptions{Model: "timestral-5m", ModelFile: modelPath, Threads: 2})
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded.Success).To(BeTrue())

		res, err := client.Forecast(ctx, &pb.ForecastRequest{
			ModelIdentity:    "timestral-5m",
			PredictionLength: 8,
			Series:           []*pb.ForecastSeries{waveSeries("a", 96, 12, 0), waveSeries("b", 48, 6, 2)},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.QuantileLevels).To(Equal([]float64{0.1, 0.5, 0.9}))
		Expect(res.Series).To(HaveLen(2))
		Expect(res.Series[0].Id).To(Equal("a"))
		Expect(res.Series[1].Id).To(Equal("b"))
		Expect(res.Series[0].Values).To(HaveLen(1 * 3 * 8))

		_, err = client.Forecast(ctx, &pb.ForecastRequest{PredictionLength: 8, Quantiles: []float64{0.42}, Series: []*pb.ForecastSeries{waveSeries("a", 96, 12, 0)}})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
		Expect(st.Message()).To(ContainSubstring("valid levels"))

		_, err = client.Forecast(ctx, &pb.ForecastRequest{ModelIdentity: "another-model", PredictionLength: 8, Series: []*pb.ForecastSeries{waveSeries("a", 96, 12, 0)}})
		Expect(err).To(HaveOccurred(), "a request for another model must be refused")
	})
})
