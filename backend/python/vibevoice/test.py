"""
A test script to test the gRPC service for VibeVoice TTS and ASR
"""
import unittest
import subprocess
import time
import os
import tempfile
import shutil
import backend_pb2
import backend_pb2_grpc

import grpc


class TestBackendServicer(unittest.TestCase):
    """
    TestBackendServicer is the class that tests the gRPC service
    """
    def setUp(self):
        """
        This method sets up the gRPC service by starting the server
        """
        self.service = subprocess.Popen(["python3", "backend.py", "--addr", "localhost:50051"])
        time.sleep(30)

    def tearDown(self) -> None:
        """
        This method tears down the gRPC service by terminating the server
        """
        self.service.terminate()
        self.service.wait()

    def test_server_startup(self):
        """
        This method tests if the server starts up successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.Health(backend_pb2.HealthMessage())
                self.assertEqual(response.message, b'OK')
        except Exception as err:
            print(err)
            self.fail("Server failed to start")
        finally:
            self.tearDown()

    def test_load_tts_model(self):
        """
        This method tests if the TTS model is loaded successfully
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="microsoft/VibeVoice-Realtime-0.5B"))
                print(response)
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed")
        finally:
            self.tearDown()

    def test_load_asr_model(self):
        """
        This method tests if the ASR model is loaded successfully with asr_mode option
        """
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                response = stub.LoadModel(backend_pb2.ModelOptions(
                    Model="microsoft/VibeVoice-ASR",
                    Options=["asr_mode:true"]
                ))
                print(response)
                self.assertTrue(response.success)
                self.assertEqual(response.message, "Model loaded successfully")
        except Exception as err:
            print(err)
            self.fail("LoadModel service failed for ASR mode")
        finally:
            self.tearDown()

    def test_tts(self):
        """
        This method tests if TTS generation works successfully
        """
        # Create a temporary directory for the output audio file
        temp_dir = tempfile.mkdtemp()
        output_file = os.path.join(temp_dir, 'output.wav')
        
        try:
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                # Load TTS model
                response = stub.LoadModel(backend_pb2.ModelOptions(Model="microsoft/VibeVoice-Realtime-0.5B"))
                self.assertTrue(response.success)
                
                # Generate TTS
                tts_request = backend_pb2.TTSRequest(
                    text="Hello, this is a test of the VibeVoice text to speech system.",
                    dst=output_file
                )
                tts_response = stub.TTS(tts_request)
                
                # Verify response
                self.assertIsNotNone(tts_response)
                self.assertTrue(tts_response.success)
                
                # Verify output file was created
                self.assertTrue(os.path.exists(output_file), f"Output file was not created: {output_file}")
                self.assertGreater(os.path.getsize(output_file), 0, "Output file is empty")
        except Exception as err:
            print(err)
            self.fail("TTS service failed")
        finally:
            self.tearDown()
            # Clean up the temporary directory
            if os.path.exists(temp_dir):
                shutil.rmtree(temp_dir)

    def test_audio_transcription(self):
        """
        This method tests if audio transcription works successfully
        """
        # Create a temporary directory for the audio file
        temp_dir = tempfile.mkdtemp()
        audio_file = os.path.join(temp_dir, 'audio.wav')
        
        try:
            # Download the audio file to the temporary directory
            print(f"Downloading audio file to {audio_file}...")
            url = "https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav"
            result = subprocess.run(
                ["wget", "-q", url, "-O", audio_file],
                capture_output=True,
                text=True
            )
            if result.returncode != 0:
                self.fail(f"Failed to download audio file: {result.stderr}")
            
            # Verify the file was downloaded
            if not os.path.exists(audio_file):
                self.fail(f"Audio file was not downloaded to {audio_file}")
            
            self.setUp()
            with grpc.insecure_channel("localhost:50051") as channel:
                stub = backend_pb2_grpc.BackendStub(channel)
                # Load the ASR model first
                load_response = stub.LoadModel(backend_pb2.ModelOptions(
                    Model="microsoft/VibeVoice-ASR-0.5B",
                    Options=["asr_mode:true"]
                ))
                self.assertTrue(load_response.success)
                
                # Perform transcription
                transcript_request = backend_pb2.TranscriptRequest(dst=audio_file)
                transcript_response = stub.AudioTranscription(transcript_request)
                
                # Print the transcribed text for debugging
                print(f"Transcribed text: {transcript_response.text}")
                print(f"Number of segments: {len(transcript_response.segments)}")
                
                # Verify response structure
                self.assertIsNotNone(transcript_response)
                self.assertIsNotNone(transcript_response.text)
                # Protobuf repeated fields return a sequence, not a list
                self.assertIsNotNone(transcript_response.segments)
                # Check if segments is iterable (has length)
                self.assertGreaterEqual(len(transcript_response.segments), 0)
                
                # Verify the transcription contains some text
                self.assertGreater(len(transcript_response.text), 0, "Transcription should not be empty")
                
                # If we got segments, verify they have the expected structure
                if len(transcript_response.segments) > 0:
                    segment = transcript_response.segments[0]
                    self.assertIsNotNone(segment.text)
                    self.assertIsInstance(segment.id, int)
                else:
                    # Even if no segments, we should have text
                    self.assertIsNotNone(transcript_response.text)
                    self.assertGreater(len(transcript_response.text), 0)
        except Exception as err:
            print(err)
            self.fail("AudioTranscription service failed")
        finally:
            self.tearDown()
            # Clean up the temporary directory
            if os.path.exists(temp_dir):
                shutil.rmtree(temp_dir)