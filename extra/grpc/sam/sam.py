#! /usr/bin/env python3

from concurrent import futures
import argparse
from enum import Enum
import os
import signal
import sys
import os
import time

import backend_pb2
import backend_pb2_grpc

import grpc

import torch
from functools import partial
from segment_anything_hq import SamAutomaticMaskGenerator
from segment_anything_hq.modeling import ImageEncoderViT, MaskDecoderHQ, PromptEncoder, Sam, TwoWayTransformer, TinyViT
import matplotlib.pyplot as plt
import numpy as np


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
PROMT_EMBED_DIM=256
IMAGE_SIZE = 1024
VIT_PATCH_SIZE=16

# Enum for sam model type
class SamModelType(str, Enum):
    default = "sam_hq_vit_h.pth"
    vit_h = "sam_hq_vit_h.pth"
    vit_l = "sam_hq_vit_l.pth"
    vit_b = "sam_hq_vit_b.pth"
    vit_tiny = "sam_hq_vit_tiny.pth"


# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer for the backend service.
    """

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", "utf-8"))

    def LoadModel(self, request, context):
        try:
            model_name = request.model_name
            if model_name not in SamModelType.__dict__.keys():
                raise Exception(f"Model name {model_name} not found in {SamModelType.__dict__.keys()}")
            
            model_path = request.model_path
            if not os.path.exists(model_path):
                raise Exception(f"Model path {model_path} does not exist")

            match model_name:
                case SamModelType.default:
                    sam = _build_sam_vit_h(checkpoint=model_path)
                case SamModelType.vit_h:
                    sam = _build_sam_vit_h(checkpoint=model_path)
                case SamModelType.vit_l:
                    sam = _build_sam_vit_l(checkpoint=model_path)
                case SamModelType.vit_b:
                    sam = _build_sam_vit_b(checkpoint=model_path)
                case SamModelType.vit_tiny:
                    sam = _build_sam_vit_tiny(checkpoint=model_path)
                case _:
                    raise Exception(f"Model name {model_name} not found in {SamModelType.__dict__.keys()}")
            # TODO No sure if this is the right way to do it
            self.model=sam

        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True, message="Model loaded successfully")

    def GenerateImage(self, request, context):
        try:
            mask_generator=SamAutomaticMaskGenerator(
                model=self.model,
                points_per_side=32,
                pred_iou_thresh=0.8,
                stability_score_thresh=0.9,
                crop_n_layers=1,
                crop_n_points_downscale_factor=2,
                min_mask_region_area=100
            )

            masks=mask_generator.generate_mask(request.image)
            masks_to_image(masks, request)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True, message="Image generated successfully")

    def PredictStream(self, request, context):
        return super().PredictStream(request, context)

def _constrcut_sam(encoder_embed_dim,encoder_depth,encoder_num_heads,encoder_global_attn_indexes,checkpoint=None):
    image_embedding_size = IMAGE_SIZE // VIT_PATCH_SIZE
    sam = Sam(
        image_encoder=ImageEncoderViT(
            depth=encoder_depth,
            embed_dim=encoder_embed_dim,
            img_size=IMAGE_SIZE,
            mlp_ratio=4,
            norm_layer=partial(torch.nn.LayerNorm, eps=1e-6),
            num_heads=encoder_num_heads,
            patch_size=VIT_PATCH_SIZE,
            qkv_bias=True,
            use_rel_pos=True,
            global_attn_indexes=encoder_global_attn_indexes,
            window_size=14,
            out_chans=PROMT_EMBED_DIM,
        ),
        prompt_encoder=PromptEncoder(
            embed_dim=PROMT_EMBED_DIM,
            image_embedding_size=(image_embedding_size, image_embedding_size),
            input_image_size=(IMAGE_SIZE, IMAGE_SIZE),
            mask_in_chans=16,
        ),
        mask_decoder=MaskDecoderHQ(
            num_multimask_outputs=3,
            transformer=TwoWayTransformer(
                depth=2,
                embedding_dim=PROMT_EMBED_DIM,
                mlp_dim=2048,
                num_heads=8,
            ),
            transformer_dim=PROMT_EMBED_DIM,
            iou_head_depth=3,
            iou_head_hidden_dim=256,
            vit_dim=encoder_embed_dim,
        ),
        pixel_mean=[123.675, 116.28, 103.53],
        pixel_std=[58.395, 57.12, 57.375],
    )

    sam.eval()
    if checkpoint is not None:
        with open(checkpoint, "rb") as f:
            device = "cuda" if torch.cuda.is_available() else "cpu"
            state_dict = torch.load(f, map_location=device)
        info = sam.load_state_dict(state_dict, strict=False)
        print(info)
    for n, p in sam.named_parameters():
        if 'hf_token' not in n and 'hf_mlp' not in n and 'compress_vit_feat' not in n and 'embedding_encoder' not in n and 'embedding_maskfeature' not in n:
            p.requires_grad = False

    return sam

def _build_sam_vit_h(checkpoint=None):
    return _constrcut_sam(encoder_embed_dim=1280,encoder_depth=32,encoder_num_heads=16,encoder_global_attn_indexes=[7,15,23,31],checkpoint=checkpoint)

def _build_sam_vit_l(checkpoint=None):
    return _constrcut_sam(encoder_embed_dim=1024,encoder_depth=24,encoder_num_heads=16,encoder_global_attn_indexes=[5,11,17,23],checkpoint=checkpoint)

def _build_sam_vit_b(checkpoint=None):
    return _constrcut_sam(encoder_embed_dim=768,encoder_depth=12,encoder_num_heads=12,encoder_global_attn_indexes=[2,5,8,11],checkpoint=checkpoint)

def _build_sam_vit_tiny(checkpoint=None):
    image_embedding_size = IMAGE_SIZE // VIT_PATCH_SIZE
    mobile_sam = Sam(
            image_encoder=TinyViT(img_size=1024, in_chans=3, num_classes=1000,
                embed_dims=[64, 128, 160, 320],
                depths=[2, 2, 6, 2],
                num_heads=[2, 4, 5, 10],
                window_sizes=[7, 7, 14, 7],
                mlp_ratio=4.,
                drop_rate=0.,
                drop_path_rate=0.0,
                use_checkpoint=False,
                mbconv_expand_ratio=4.0,
                local_conv_size=3,
                layer_lr_decay=0.8
            ),
            prompt_encoder=PromptEncoder(
            embed_dim=PROMT_EMBED_DIM,
            image_embedding_size=(image_embedding_size, image_embedding_size),
            input_image_size=(IMAGE_SIZE, IMAGE_SIZE),
            mask_in_chans=16,
            ),
            mask_decoder=MaskDecoderHQ(
                    num_multimask_outputs=3,
                    transformer=TwoWayTransformer(
                    depth=2,
                    embedding_dim=PROMT_EMBED_DIM,
                    mlp_dim=2048,
                    num_heads=8,
                ),
                transformer_dim=PROMT_EMBED_DIM,
                iou_head_depth=3,
                iou_head_hidden_dim=256,
                vit_dim=160,
            ),
            pixel_mean=[123.675, 116.28, 103.53],
            pixel_std=[58.395, 57.12, 57.375],
        )

    mobile_sam.eval()
    if checkpoint is not None:
        with open(checkpoint, "rb") as f:
            device = "cuda" if torch.cuda.is_available() else "cpu"
            state_dict = torch.load(f, map_location=device)
        info = mobile_sam.load_state_dict(state_dict, strict=False)
        print(info)
    for n, p in mobile_sam.named_parameters():
        if 'hf_token' not in n and 'hf_mlp' not in n and 'compress_vit_feat' not in n and 'embedding_encoder' not in n and 'embedding_maskfeature' not in n:
            p.requires_grad = False
    return mobile_sam

def masks_to_image(anns, request):
    if len(anns)==0:
        return
    sorted_anns = sorted(anns, key=(lambda x: x['area']), reverse=True)
    ax = plt.gca()
    ax.set_autoscale_on(False)

    img = np.ones((sorted_anns[0]['segmentation'].shape[0], sorted_anns[0]['segmentation'].shape[1], 4))
    img[:,:,3] = 0
    for ann in sorted_anns:
        m = ann['segmentation']
        color_mask = np.concatenate([np.random.random(3), [0.35]])
        img[m] = color_mask
    ax.imshow(img)
    plt.axis('off')
    plt.imsave(request.dst, img)
    

def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("Received termination signal. Shutting down...")
        server.stop(0)
        sys.exit(0)

    # Set the signal handlers for SIGINT and SIGTERM
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    serve(args.addr)