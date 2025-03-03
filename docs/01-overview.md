# Overview
The `imaging` service captures a image frame using `gstreamer`, does some preprocessing and finds the edges of the track. While we won't go too in-depth here, the preprocessing of the image does roughly the following:

1. Convert the RGB image to grayscale.

![original_image](https://github.com/user-attachments/assets/3e289b3e-bbf6-4e8f-a0ae-c09787e71934)

![after_grayscale](https://github.com/user-attachments/assets/a9e00fec-eb06-43d1-ad5d-3b202db54001)


2. Convert the grayscale image to black and white via [*thresholding*](https://en.wikipedia.org/wiki/Thresholding_(image_processing)). The key parameter here is the threshold value which is fixed.

![after_thresholding](https://github.com/user-attachments/assets/69987076-3d72-47d3-bed6-b540323e7865)


Notice how there is clearly a white track, but with some additional artifacts from the floor's reflection. There are many techniques in dealing with such artifacts as well as ways in making the algorithm that follows more robust, however we won't dive into that here.

Then, the algorithm for finding the edges is rather simple. It takes as input the binary image from Step 2 and makes a vertical scan up from the bottom center of the image, returning the y-coordinate of the first black pixel that it finds. If it doesn't find a black pixel after crossing the bottom half of the image, it simply returns the middle of the screen. This is then the place where a horizontal scan is taken with a similar approach to find the left and right edges of the track.

![with_points](https://github.com/user-attachments/assets/5b3c7df6-874a-4ec9-9d15-cb725457faf6)


