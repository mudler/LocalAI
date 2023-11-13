# Common commands about conda environment

## Create a new empty conda environment

```
conda create --name <env-name> python=<your version> -y

conda create --name autogptq python=3.11 -y
```

## To activate the environment

As of conda 4.4
```
conda activate autogptq
```

The conda version older than 4.4

```
source activate autogptq
```

## Install the packages to your environment

Sometimes you need to install the packages from the conda-forge channel

By using `conda`
```
conda install <your-package-name>

conda install -c conda-forge <your package-name>
```

Or by using `pip`
```
pip install <your-package-name>
```
