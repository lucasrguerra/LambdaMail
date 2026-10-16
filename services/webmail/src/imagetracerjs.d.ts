/** imagetracerjs ships no types; only the call the studio makes is described. */
declare module "imagetracerjs" {
  const ImageTracer: {
    imagedataToSVG(
      raster: { width: number; height: number; data: Uint8ClampedArray },
      options?: Record<string, number | string | boolean>,
    ): string;
  };
  export default ImageTracer;
}
