# vegeta-break

## Basics
Vegeta-break is a comandline tool for discovering the max requests per second a service can handle while staying below a specific latency. It also outputs detailed latency curve files that can be load into [HdrHistogram Plotter](hdrhistogram.github.io/HdrHistogram/plotFiles.html) in order to better understand how the application acts under stress. The tool does this by amping up the number of requests per second of the application while insuring it is under a max latency.

 ```
 Usage: vegeta-break [OPTIONS] url
  -duration duration
    	Duration for each latency test (default 1m0s)
  -percentile float
    	The percentile that latency is measured at (default 99)
  -rps int
    	Starting requests per second (default 20)
  -scaleup-percent float
    	Percent of duration to scale up rps before each latency test (default 0.1)
  -scaleup-steps int
    	number of steps to go from 0 to max rps (default 10)
  -sla duration
    	Max acceptable latency (default 500ms)
 ```

 ## Inspiration and Background
 The work by Gil Tene is a large inspiration for vegeta-break. Gil Tene teaches just how bad most latency measuring tools are through his talk [How NOT to Measure Latency](https://www.youtube.com/watch?v=lJ8ydIuPFeU). Other resources that talk about this issue in shorter article form are [Everything You Know About Latency Is Wrong](https://bravenewgeek.com/everything-you-know-about-latency-is-wrong/) and [Your Load Generator is Probably Lying to You](http://highscalability.com/blog/2015/10/5/your-load-generator-is-probably-lying-to-you-take-the-red-pi.html). I highly suggest you watch the talk if not at least read the articles. They show just how wrongly we measure and interpret latency. I hope that this tool can help people better compare websites changes based on latency and requests per second.

 The core to this project is a tool called vegeta. Vegeta is a load testing tool that gets around the latency calculations issues that other tools have by asyncronously requesting for pages. If vegeta does not have enoug workers, it will spin up a new worker to ensure that request are sent on time. Thus, for vegeta, 50 requests per second means a request will be sent out every 20ms no matter the current latency and state of other requests.

Lastly, [HTTP Load Testing with Vegeta (and a dash of Python)](https://serialized.net/2017/06/load-testing-with-vegeta-and-python/) is the basis of how I decided to test webpages. The author of this post actually made a script called vegeta-break. I took that script and expanded upon it to build the tool found here.

## What values should you use for sla and latency percentile?
As a general rule of thumb, take the latency that you want X% of users to see and the add one to two 9s onto the end. For example, if you want 99% of users to see a latency of less than 1 second, then set percentile to 99.9 or 99.99 and sla to 1s.