package main

import (
	"fmt"
	"image"
	"os"
	"time"

	pb_output "github.com/VU-ASE/rovercom/packages/go/outputs"
	roverlib "github.com/VU-ASE/roverlib-go/src"
	"gocv.io/x/gocv"
	"google.golang.org/protobuf/proto"

	"github.com/rs/zerolog/log"
)

type SliceDescriptor struct {
	Start int // Start index of the array
	End   int // End index of the array
}

// This function will cast a vertical scan on the given x-line, starting at coordinate Y and proceeding onwards (= towards a smaller Y)
// it returns the Y-coordinate of the first black pixel it encounters
func verticalScanUp(image *gocv.Mat, x int, startY int) int {
	y := startY
	for y >= 0 {
		if image.GetUCharAt(y, x) == 0 {
			return y
		}
		y--
	}
	return y + 1
}

// This function scans the slice for points that are full white (non-black) (after thresholding)
// It returns an array of descriptions of the consecutive white points
// r.i.p. mrbuggy :(
func getConsecutiveWhitePointsFromSlice(imageSlice *gocv.Mat) []SliceDescriptor {
	res := []SliceDescriptor{}

	var currentConsecutive *SliceDescriptor = nil

	for i := 0; i < imageSlice.Cols()-1; i++ {
		currentByte := imageSlice.GetVecbAt(0, i)[0]

		// byte(0) indicates black, byte(255) indicates white
		if currentByte != byte(0) {
			// Current point is a white point. Is there already a consecutive array?
			if currentConsecutive == nil {
				// No, create a new one
				currentConsecutive = &SliceDescriptor{
					Start: i,
					End:   i,
				}
			} else {
				// Yes, extend the current one
				currentConsecutive.End = i
			}
		} else {
			// Current point is black. Is there a consecutive array?
			if currentConsecutive != nil {
				// Yes, add it to the result, if it's at minimum 1 pixel wide
				if currentConsecutive.End-currentConsecutive.Start > 0 {
					res = append(res, *currentConsecutive)
				}
				currentConsecutive = nil
			}
		}
	}

	// We reached the right edge of the image. If there is a consecutive array, add it to the result
	if currentConsecutive != nil && currentConsecutive.End-currentConsecutive.Start > 0 {
		res = append(res, *currentConsecutive)
	}

	return res
}

// This function takes an array of slice descriptors and finds the one with the most consecutive white pixels
// It returns nil if no such slice is found
// The second parameter is the preferred X. If a slice is found that contains this preferred x, this slice is returned
// and not the longest
func getLongestConsecutiveWhiteSlice(sliceDescriptors []SliceDescriptor, preferredX int) *SliceDescriptor {
	if len(sliceDescriptors) == 0 {
		return nil
	}

	longest := sliceDescriptors[0]
	for _, desc := range sliceDescriptors {
		// If this slice contains the preferredX, choose this one
		if preferredX > desc.Start && preferredX < desc.End {
			log.Debug().Int("preferredX", preferredX).Msg("Returned slice containing preferred X, instead of longest slice")
			return &desc
		}

		if (desc.End - desc.Start) > (longest.End - longest.Start) {
			longest = desc
		}
	}

	return &longest
}

// Global values that can be tuned OTA
var thresholdValue int

// Runs the program logic
func run(service roverlib.Service, configuration *roverlib.ServiceConfiguration) error {
	if configuration == nil {
		return fmt.Errorf("configuration cannot be accessed")
	}

	//
	// Get configuration values from service.json
	//
	gstPipeline, err := configuration.GetStringSafe("gstreamer-pipeline")
	if err != nil {
		log.Err(err).Msg("Failed to get gstreamer-pipeline from tuning. Is it defined in service.yaml?")
		return err
	}
	thFloat, err := configuration.GetFloatSafe("threshold-value")
	if err != nil {
		return err
	}
	thresholdValue = int(thFloat)
	// Fetch width to put in gstreaqmer pipeline
	imgWidthFloat, err := configuration.GetFloatSafe("img-width")
	if err != nil {
		return err
	}
	imgWidth := int(imgWidthFloat)
	// Fetch height to put in gstreamer pipeline
	imgHeightFloat, err := configuration.GetFloatSafe("img-height")
	if err != nil {
		return err
	}
	imgHeight := int(imgHeightFloat)
	// Fetch image fps to put in gstreamer pipeline
	imgFpsFloat, err := configuration.GetFloatSafe("img-fps")
	if err != nil {
		return err
	}
	imgFps := int(imgFpsFloat)
	// Create the gstreamer pipeline with the fetched parameters
	gstPipeline = fmt.Sprintf(gstPipeline, imgWidth, imgHeight, imgFps)
	log.Info().Str("pipeline", gstPipeline).Msg("Using gstreamer pipeline")

	// Create socket to send images to
	imageOutput := service.GetWriteStream("path")
	if err != nil {
		return err
	}

	// Open video capture using gstreamer pipeline
	cam, err := gocv.OpenVideoCapture(gstPipeline)
	if err != nil {
		return err
	}
	defer cam.Close()

	// Complete images are stored in this mat
	buf := gocv.NewMat()
	defer buf.Close()

	// Y coordinate of the horizontal slice used for steering
	const sliceY = 400

	// Start with the middle of the image as the preferred X to find the white slice
	// (assuming that the car starts on the middle of the track)
	preferredX := imgWidth / 2

	for {
		if ok := cam.Read(&buf); !ok {
			log.Warn().Err(err).Msg("Error reading from camera")
			continue
		}
		if buf.Empty() {
			continue
		}
		imgWidth := buf.Cols()
		imgHeight := buf.Rows()

		log.Info().Int("width", imgWidth).Int("height", imgHeight).Msg("Read image")

		if thresholdValue > 0 {
			// Convert the image to grayscale (for thresholding)
			gocv.CvtColor(buf, &buf, gocv.ColorBGRToGray)
			// Apply thresholding
			gocv.Threshold(buf, &buf, float32(thresholdValue), 255.0, gocv.ThresholdBinary+gocv.ThresholdOtsu)
			// Apply dilation
			kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Pt(5, 5))
			gocv.Dilate(buf, &buf, kernel)
			gocv.Erode(buf, &buf, kernel)
		}

		var longestConsecutive *SliceDescriptor = nil

		newBarY := verticalScanUp(&buf, preferredX, imgHeight-1) + 2
		if newBarY >= imgHeight {
			newBarY = imgHeight - 1
		}

		usedSlice := uint32(newBarY)
		if usedSlice < uint32(sliceY) {
			usedSlice = uint32(sliceY)
		}

		for uint32(usedSlice) < (uint32(imgHeight)-1) && (longestConsecutive == nil) {
			usedSlice += 10

			// Take a slice that is used to steer on
			horizontalSlice := buf.Region(image.Rect(0, sliceY, imgWidth, sliceY+1))
			// Find the consecutive white points
			sliceDescriptors := getConsecutiveWhitePointsFromSlice(&horizontalSlice)
			// Find the longest consecutive white slice
			longestConsecutive = getLongestConsecutiveWhiteSlice(sliceDescriptors, preferredX)

			if longestConsecutive != nil && (preferredX < longestConsecutive.Start || preferredX > longestConsecutive.End) {
				longestConsecutive = nil
			}
			horizontalSlice.Clone() // avoid memory leaks
		}

		// Create a canvas that can be drawn on
		canvasObjects := make([]*pb_output.CanvasObject, 0)
		// Draw points where the longest consecutive slice starts, ends and the middle
		if longestConsecutive != nil {
			middleX := (longestConsecutive.Start + longestConsecutive.End) / 2
			preferredX = middleX

			// Draw start
			canvasObjects = append(canvasObjects, &pb_output.CanvasObject{
				Object: &pb_output.CanvasObject_Circle_{
					Circle: &pb_output.CanvasObject_Circle{
						Center: &pb_output.CanvasObject_Point{
							X: uint32(longestConsecutive.Start),
							Y: sliceY,
						},
						Radius: 1,
					},
				},
			})
			// Draw end
			canvasObjects = append(canvasObjects, &pb_output.CanvasObject{
				Object: &pb_output.CanvasObject_Circle_{
					Circle: &pb_output.CanvasObject_Circle{
						Center: &pb_output.CanvasObject_Point{
							X: uint32(longestConsecutive.End),
							Y: sliceY,
						},
						Radius: 1,
					},
				},
			})
			// Draw middle
			canvasObjects = append(canvasObjects, &pb_output.CanvasObject{
				Object: &pb_output.CanvasObject_Circle_{
					Circle: &pb_output.CanvasObject_Circle{
						Center: &pb_output.CanvasObject_Point{
							X: uint32(middleX),
							Y: sliceY,
						},
						Radius: 1,
					},
				},
			})
		}

		canvas := pb_output.Canvas{
			Objects: canvasObjects,
			Width:   uint32(imgWidth),
			Height:  uint32(imgHeight),
		}

		// used for JPEG compression
		var compressionParams [2]int
		compressionParams[0] = gocv.IMWriteJpegQuality
		compressionParams[1] = 30 // the quality
		// Convert the image to JPEG bytes
		imgBytes, err := gocv.IMEncodeWithParams(".jpg", buf, compressionParams[:])
		if err != nil {
			log.Err(err).Msg("Error encoding image")
			return err
		}

		// Create the trajectory, (currently it is just the middle of the longest consecutive slice)
		trajectory_points := make([]*pb_output.CameraSensorOutput_Trajectory_Point, 0)
		if longestConsecutive != nil {
			middleX := (longestConsecutive.Start + longestConsecutive.End) / 2
			trajectory_points = append(trajectory_points, &pb_output.CameraSensorOutput_Trajectory_Point{
				X: int32(middleX),
				Y: sliceY,
			})

			log.Debug().Int("x", middleX).Msg("Trajectory added")
		} else {
			log.Debug().Msg("No trajectory added")
		}

		// Make it a sensor output
		output := pb_output.SensorOutput{
			SensorId:  25,
			Timestamp: uint64(time.Now().UnixMilli()),
			SensorOutput: &pb_output.SensorOutput_CameraOutput{
				CameraOutput: &pb_output.CameraSensorOutput{
					DebugFrame: &pb_output.CameraSensorOutput_DebugFrame{
						Jpeg:   imgBytes.GetBytes(),
						Canvas: &canvas,
					},
					Trajectory: &pb_output.CameraSensorOutput_Trajectory{
						Points: trajectory_points,
						Width:  uint32(imgWidth),
						Height: uint32(imgHeight),
					},
				},
			},
		}
		outputBytes, err := proto.Marshal(&output)
		if err != nil {
			log.Err(err).Msg("Error marshalling sensor output")
			continue
		}

		// Send the image
		err = imageOutput.WriteBytes(outputBytes)
		if err != nil {
			log.Err(err).Int("byte len", len(outputBytes)).Msg("Error sending image")
			return err
		}

		log.Debug().Msg("Sent image")
	}
}

func onTerminate(sig os.Signal) error {
	log.Info().Msg("Terminating")

	return nil
}

// Used to start the program with the correct arguments
func main() {
	roverlib.Run(run, onTerminate)
}
